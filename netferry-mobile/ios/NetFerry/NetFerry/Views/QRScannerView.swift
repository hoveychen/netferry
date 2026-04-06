import AVFoundation
import NetFerryEngine
import SwiftUI

// MARK: - QR Scanner View

struct QRScannerView: View {
    @Environment(ProfileStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    @State private var chunks: [Int: String] = [:]
    @State private var totalChunks: Int = 0
    @State private var errorMessage: String?
    @State private var showingError = false
    @State private var importedProfile: Profile?
    @State private var showingSuccess = false
    @State private var cameraPermission: AVAuthorizationStatus = .notDetermined

    var body: some View {
        NavigationStack {
            ZStack {
                if cameraPermission == .authorized {
                    CameraPreview(onQRCodeDetected: handleQRCode)
                        .ignoresSafeArea()

                    scanOverlay
                } else if cameraPermission == .denied || cameraPermission == .restricted {
                    cameraPermissionDenied
                } else {
                    ProgressView("Requesting camera access...")
                }
            }
            .navigationTitle("Scan QR Code")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
            }
            .alert("Scan Error", isPresented: $showingError) {
                Button("OK", role: .cancel) {}
            } message: {
                Text(errorMessage ?? "Unknown error")
            }
            .alert("Profile Imported", isPresented: $showingSuccess) {
                Button("OK") {
                    dismiss()
                }
            } message: {
                if let profile = importedProfile {
                    Text("Successfully imported \"\(profile.displayName)\".")
                } else {
                    Text("Profile imported successfully.")
                }
            }
            .task {
                await requestCameraPermission()
            }
        }
    }

    // MARK: - Overlay

    private var scanOverlay: some View {
        VStack {
            Spacer()

            // Viewfinder frame
            RoundedRectangle(cornerRadius: 16)
                .strokeBorder(.white.opacity(0.6), lineWidth: 2)
                .frame(width: 250, height: 250)
                .background(.clear)

            Spacer().frame(height: 32)

            // Progress indicator
            VStack(spacing: 8) {
                if totalChunks > 0 {
                    Text("Scanned \(chunks.count)/\(totalChunks)")
                        .font(.headline)
                        .foregroundStyle(.white)

                    ProgressView(value: Double(chunks.count), total: Double(totalChunks))
                        .tint(.white)
                        .frame(width: 200)
                } else {
                    Text("Point camera at a NetFerry QR code")
                        .font(.subheadline)
                        .foregroundStyle(.white)
                }
            }
            .padding()
            .background(.black.opacity(0.6), in: RoundedRectangle(cornerRadius: 12))

            Spacer().frame(height: 48)
        }
    }

    // MARK: - Permission denied

    private var cameraPermissionDenied: some View {
        ContentUnavailableView {
            Label("Camera Access Required", systemImage: "camera.fill")
        } description: {
            Text("NetFerry needs camera access to scan QR codes. Please enable it in Settings.")
        } actions: {
            Button("Open Settings") {
                if let url = URL(string: UIApplication.openSettingsURLString) {
                    UIApplication.shared.open(url)
                }
            }
            .buttonStyle(.borderedProminent)
        }
    }

    // MARK: - Camera Permission

    private func requestCameraPermission() async {
        let status = AVCaptureDevice.authorizationStatus(for: .video)
        if status == .notDetermined {
            let granted = await AVCaptureDevice.requestAccess(for: .video)
            cameraPermission = granted ? .authorized : .denied
        } else {
            cameraPermission = status
        }
    }

    // MARK: - QR Handling

    private func handleQRCode(_ code: String) {
        // Parse the chunk in Swift (gomobile cannot export functions with >2 returns).
        // Format: "NF:{index}/{total}:{data}"
        guard let parsed = parseQRChunk(code) else {
            return // not a NetFerry QR code, silently ignore
        }

        let (index, total, _) = parsed

        // First chunk sets the expected total
        if totalChunks == 0 {
            totalChunks = total
        } else if total != totalChunks {
            showError("Inconsistent QR codes: expected \(totalChunks) chunks but got \(total)")
            return
        }

        // Store the raw QR string keyed by index
        guard chunks[index] == nil else {
            return // already scanned this chunk
        }
        chunks[index] = code

        // Check if all chunks are collected
        if chunks.count == totalChunks {
            importProfile()
        }
    }

    /// Parse "NF:{index}/{total}:{data}" and return (index, total, data) or nil.
    private func parseQRChunk(_ chunk: String) -> (Int, Int, String)? {
        guard chunk.hasPrefix("NF:") else { return nil }

        let rest = String(chunk.dropFirst(3))
        guard let slashIndex = rest.firstIndex(of: "/") else { return nil }

        let afterSlash = rest[rest.index(after: slashIndex)...]
        guard let colonIndex = afterSlash.firstIndex(of: ":") else { return nil }

        guard let idx = Int(rest[rest.startIndex..<slashIndex]),
              let tot = Int(afterSlash[afterSlash.startIndex..<colonIndex]) else {
            return nil
        }

        let data = String(afterSlash[afterSlash.index(after: colonIndex)...])
        return (idx, tot, data)
    }

    private func importProfile() {
        // Build the JSON array of raw QR strings, ordered by index
        let orderedChunks = (1...totalChunks).compactMap { chunks[$0] }
        guard orderedChunks.count == totalChunks else {
            showError("Missing QR chunks")
            return
        }

        guard let jsonData = try? JSONSerialization.data(withJSONObject: orderedChunks),
              let chunksJSON = String(data: jsonData, encoding: .utf8) else {
            showError("Failed to build chunks JSON")
            return
        }

        // Call the Go engine to reassemble and decrypt
        let json: String
        do {
            var error: NSError?
            let result = MobileImportFromQR(chunksJSON, &error)
            if let error { throw error }
            json = result
        } catch {
            showError("Decryption failed: \(error.localizedDescription)")
            return
        }

        guard !json.isEmpty else {
            showError("Decryption returned empty result")
            return
        }

        // Parse profile JSON
        guard let data = json.data(using: .utf8) else {
            showError("Invalid profile data")
            return
        }

        do {
            let profile = try JSONDecoder().decode(Profile.self, from: data)
            store.save(profile)
            importedProfile = profile
            showingSuccess = true
        } catch {
            showError("Failed to parse profile: \(error.localizedDescription)")
        }
    }

    private func showError(_ message: String) {
        errorMessage = message
        showingError = true
    }
}

// MARK: - Camera Preview (UIViewControllerRepresentable)

private struct CameraPreview: UIViewControllerRepresentable {
    let onQRCodeDetected: (String) -> Void

    func makeUIViewController(context: Context) -> CameraViewController {
        let controller = CameraViewController()
        controller.onQRCodeDetected = onQRCodeDetected
        return controller
    }

    func updateUIViewController(_ uiViewController: CameraViewController, context: Context) {
        uiViewController.onQRCodeDetected = onQRCodeDetected
    }
}

// MARK: - Camera View Controller

private final class CameraViewController: UIViewController {
    var onQRCodeDetected: ((String) -> Void)?

    private let captureSession = AVCaptureSession()
    private var previewLayer: AVCaptureVideoPreviewLayer?
    private let metadataQueue = DispatchQueue(label: "com.netferry.qr-metadata")
    private let metadataDelegate = MetadataDelegate()

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black
        setupCamera()
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        previewLayer?.frame = view.bounds
    }

    override func viewWillAppear(_ animated: Bool) {
        super.viewWillAppear(animated)
        if !captureSession.isRunning {
            DispatchQueue.global(qos: .userInitiated).async { [weak self] in
                self?.captureSession.startRunning()
            }
        }
    }

    override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        if captureSession.isRunning {
            DispatchQueue.global(qos: .userInitiated).async { [weak self] in
                self?.captureSession.stopRunning()
            }
        }
    }

    private func setupCamera() {
        guard let device = AVCaptureDevice.default(for: .video),
              let input = try? AVCaptureDeviceInput(device: device) else {
            return
        }

        if captureSession.canAddInput(input) {
            captureSession.addInput(input)
        }

        let metadataOutput = AVCaptureMetadataOutput()
        if captureSession.canAddOutput(metadataOutput) {
            captureSession.addOutput(metadataOutput)
            metadataDelegate.onQRCodeDetected = onQRCodeDetected
            metadataOutput.setMetadataObjectsDelegate(metadataDelegate, queue: metadataQueue)
            metadataOutput.metadataObjectTypes = [.qr]
        }

        let layer = AVCaptureVideoPreviewLayer(session: captureSession)
        layer.videoGravity = .resizeAspectFill
        layer.frame = view.bounds
        view.layer.addSublayer(layer)
        previewLayer = layer

        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            self?.captureSession.startRunning()
        }
    }

}

// Separate delegate to avoid @MainActor isolation conflict with AVCaptureMetadataOutputObjectsDelegate.
private final class MetadataDelegate: NSObject, AVCaptureMetadataOutputObjectsDelegate, @unchecked Sendable {
    // @unchecked Sendable on the class handles the thread-safety contract.
    var onQRCodeDetected: ((String) -> Void)?
    private var recentCodes = Set<String>()

    func metadataOutput(
        _ output: AVCaptureMetadataOutput,
        didOutput metadataObjects: [AVMetadataObject],
        from connection: AVCaptureConnection
    ) {
        for object in metadataObjects {
            guard let readable = object as? AVMetadataMachineReadableCodeObject,
                  let code = readable.stringValue,
                  code.hasPrefix("NF:"),
                  !recentCodes.contains(code) else {
                continue
            }

            recentCodes.insert(code)

            let callback = onQRCodeDetected
            DispatchQueue.main.async {
                callback?(code)
            }
        }
    }
}
