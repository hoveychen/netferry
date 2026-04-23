import Foundation
import NetworkExtension
import Observation

// Matches the PacketTunnel extension's App Group + key. The extension writes the
// latest engine/setup failure message here so the main app can surface it — the
// regular `startVPNTunnel()` throw path only covers NE-level errors.
private let kSharedAppGroup = "group.com.netferry.app"
private let kLastErrorKey = "lastConnectionError"

@Observable
@MainActor
final class VPNManager {
    private(set) var status: NEVPNStatus = .disconnected
    private(set) var connectedProfileID: UUID?
    private(set) var stats: TunnelStats = TunnelStats()
    private(set) var deployProgress: DeployProgress?
    /// Most recent tunnel-extension failure message read from the App Group.
    /// Consumers show an alert, then call `dismissLastError()` to clear.
    private(set) var lastError: String?

    private var manager: NETunnelProviderManager?
    // nonisolated(unsafe) allows deinit to access these for cleanup.
    nonisolated(unsafe) private var statusObserver: NSObjectProtocol?
    nonisolated(unsafe) private var statsTimer: Timer?
    nonisolated(unsafe) private var deployTimer: Timer?

    init() {
        startObservingStatus()
        Task {
            await loadManager()
        }
    }

    deinit {
        if let observer = statusObserver {
            NotificationCenter.default.removeObserver(observer)
        }
        statsTimer?.invalidate()
        deployTimer?.invalidate()
    }

    var isConnected: Bool {
        status == .connected
    }

    var isTransitioning: Bool {
        status == .connecting || status == .disconnecting || status == .reasserting
    }

    var statusText: String {
        switch status {
        case .disconnected: "Disconnected"
        case .connecting: "Connecting..."
        case .connected: "Connected"
        case .disconnecting: "Disconnecting..."
        case .reasserting: "Reconnecting..."
        case .invalid: "Invalid"
        @unknown default: "Unknown"
        }
    }

    private func loadManager() async {
        do {
            let managers = try await NETunnelProviderManager.loadAllFromPreferences()
            self.manager = managers.first
            if let mgr = self.manager {
                self.status = mgr.connection.status
            }
        } catch {
            NSLog("VPNManager: loadAllFromPreferences error: \(error)")
        }
    }

    private func loadOrCreateManager() async throws -> NETunnelProviderManager {
        let managers = try await NETunnelProviderManager.loadAllFromPreferences()
        if let existing = managers.first {
            self.manager = existing
            return existing
        }
        let newManager = NETunnelProviderManager()
        newManager.localizedDescription = "NetFerry"
        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = "com.netferry.app.PacketTunnel"
        proto.serverAddress = "NetFerry"
        newManager.protocolConfiguration = proto
        newManager.isEnabled = true
        try await newManager.saveToPreferences()
        try await newManager.loadFromPreferences()
        self.manager = newManager
        return newManager
    }

    func connect(profile: Profile) async throws {
        // Wipe stale extension error so a fresh attempt's alert doesn't
        // display leftovers from a previous failed session.
        UserDefaults(suiteName: kSharedAppGroup)?.removeObject(forKey: kLastErrorKey)
        lastError = nil

        let mgr = try await loadOrCreateManager()

        guard let proto = mgr.protocolConfiguration as? NETunnelProviderProtocol else {
            throw VPNError.invalidConfiguration
        }

        proto.serverAddress = profile.remote
        proto.providerConfiguration = [
            "configJSON": profile.toConfigJSON(),
            "profileID": profile.id.uuidString
        ]

        mgr.protocolConfiguration = proto
        mgr.isEnabled = true
        mgr.localizedDescription = "NetFerry - \(profile.displayName)"

        try await mgr.saveToPreferences()
        try await mgr.loadFromPreferences()

        try mgr.connection.startVPNTunnel()
        connectedProfileID = profile.id
    }

    func dismissLastError() {
        lastError = nil
        UserDefaults(suiteName: kSharedAppGroup)?.removeObject(forKey: kLastErrorKey)
    }

    func disconnect() {
        manager?.connection.stopVPNTunnel()
        connectedProfileID = nil
    }

    private func startObservingStatus() {
        statusObserver = NotificationCenter.default.addObserver(
            forName: .NEVPNStatusDidChange,
            object: nil,
            queue: .main
        ) { [weak self] notification in
            guard let self else { return }
            let vpnStatus = (notification.object as? NEVPNConnection)?.status
            Task { @MainActor in
                guard let vpnStatus else { return }
                self.status = vpnStatus

                if vpnStatus == .connecting || vpnStatus == .reasserting {
                    self.startDeployPolling()
                } else {
                    self.stopDeployPolling()
                }

                if vpnStatus == .connected {
                    self.startStatsPolling()
                    self.deployProgress = nil
                } else if vpnStatus == .disconnected {
                    self.stopStatsPolling()
                    self.stats = TunnelStats()
                    self.deployProgress = nil
                    // Extension writes failure details to the shared App Group
                    // when startTunnel/engine.start fails. Pick them up here so
                    // the UI can surface a specific message instead of a blank
                    // bounce back to the profile list.
                    if let message = UserDefaults(suiteName: kSharedAppGroup)?.string(forKey: kLastErrorKey),
                       !message.isEmpty {
                        self.lastError = message
                    }
                }
            }
        }
    }

    private func startStatsPolling() {
        stopStatsPolling()
        statsTimer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] _ in
            Task { @MainActor in
                self?.pollStats()
            }
        }
    }

    private func stopStatsPolling() {
        statsTimer?.invalidate()
        statsTimer = nil
    }

    private func pollStats() {
        guard let session = manager?.connection as? NETunnelProviderSession else { return }
        do {
            try session.sendProviderMessage("stats".data(using: .utf8)!) { [weak self] response in
                guard let self, let data = response, let json = String(data: data, encoding: .utf8) else { return }
                if let newStats = TunnelStats.from(json: json) {
                    Task { @MainActor in
                        self.stats = newStats
                    }
                }
            }
        } catch {
            NSLog("VPNManager: sendProviderMessage error: \(error)")
        }
    }

    private func startDeployPolling() {
        stopDeployPolling()
        deployTimer = Timer.scheduledTimer(withTimeInterval: 0.3, repeats: true) { [weak self] _ in
            Task { @MainActor in
                self?.pollDeploy()
            }
        }
    }

    private func stopDeployPolling() {
        deployTimer?.invalidate()
        deployTimer = nil
    }

    private func pollDeploy() {
        guard let session = manager?.connection as? NETunnelProviderSession else { return }
        do {
            try session.sendProviderMessage("deploy".data(using: .utf8)!) { [weak self] response in
                guard let self, let data = response, let json = String(data: data, encoding: .utf8) else { return }
                if let progress = DeployProgress.from(json: json) {
                    Task { @MainActor in
                        self.deployProgress = progress
                    }
                }
            }
        } catch {
            // Extension may not be ready yet during early connecting phase.
        }
    }

    enum VPNError: LocalizedError {
        case invalidConfiguration

        var errorDescription: String? {
            switch self {
            case .invalidConfiguration:
                return L("vpn.error.invalidConfiguration")
            }
        }
    }
}

struct DeployProgress: Equatable {
    let sent: Int64
    let total: Int64
    let reason: String

    var fraction: Double {
        total > 0 ? Double(sent) / Double(total) : 0
    }

    var percent: Int {
        Int(fraction * 100)
    }

    var isUploading: Bool {
        total > 0 && reason != "up-to-date"
    }

    static func from(json: String) -> DeployProgress? {
        guard let data = json.data(using: .utf8),
              let dict = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let sent = dict["sent"] as? Int64,
              let total = dict["total"] as? Int64,
              let reason = dict["reason"] as? String else {
            return nil
        }
        return DeployProgress(sent: sent, total: total, reason: reason)
    }

    static func formatBytes(_ bytes: Int64) -> String {
        if bytes < 1024 { return "\(bytes) B" }
        if bytes < 1024 * 1024 { return String(format: "%.1f KB", Double(bytes) / 1024) }
        if bytes < 1024 * 1024 * 1024 { return String(format: "%.1f MB", Double(bytes) / (1024 * 1024)) }
        return String(format: "%.2f GB", Double(bytes) / (1024 * 1024 * 1024))
    }
}
