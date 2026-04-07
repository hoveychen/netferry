import SwiftUI
import UniformTypeIdentifiers
import NetFerryEngine

struct ProfileListView: View {
    @Environment(ProfileStore.self) private var store
    @Environment(VPNManager.self) private var vpnManager
    @State private var showingNewProfile = false
    @State private var showingQRScanner = false
    @State private var showingFileImporter = false
    @State private var selectedProfile: Profile?
    @State private var connectError: String?
    @State private var showingError = false
    @State private var importError: String?
    @State private var showingImportError = false

    var body: some View {
        NavigationStack {
            Group {
                if store.profiles.isEmpty {
                    emptyState
                } else {
                    profileList
                }
            }
            .navigationTitle(L("nav.profiles"))
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    HStack(spacing: 16) {
                        Button {
                            showingFileImporter = true
                        } label: {
                            Image(systemName: "doc.badge.plus")
                        }
                        Button {
                            showingQRScanner = true
                        } label: {
                            Image(systemName: "qrcode.viewfinder")
                        }
                        Button {
                            showingNewProfile = true
                        } label: {
                            Image(systemName: "plus")
                        }
                    }
                }
            }
            .sheet(isPresented: $showingNewProfile) {
                NavigationStack {
                    ProfileDetailView(profile: Profile())
                }
                .environment(store)
            }
            .sheet(item: $selectedProfile) { profile in
                NavigationStack {
                    ProfileDetailView(profile: profile)
                }
                .environment(store)
            }
            .sheet(isPresented: $showingQRScanner) {
                QRScannerView()
                    .environment(store)
            }
            .fileImporter(
                isPresented: $showingFileImporter,
                allowedContentTypes: [.data],
                allowsMultipleSelection: false
            ) { result in
                handleFileImport(result)
            }
            .alert("Connection Error", isPresented: $showingError) {
                Button("OK", role: .cancel) {}
            } message: {
                Text(connectError ?? "Unknown error")
            }
            .alert("Import Error", isPresented: $showingImportError) {
                Button("OK", role: .cancel) {}
            } message: {
                Text(importError ?? "")
            }
        }
    }

    private var emptyState: some View {
        ContentUnavailableView {
            Label(L("profiles.empty.title"), systemImage: "network.slash")
        } description: {
            Text(L("profiles.empty.desc"))
        } actions: {
            VStack(spacing: 12) {
                Button(L("profiles.add")) {
                    showingNewProfile = true
                }
                .buttonStyle(.borderedProminent)

                Button {
                    showingQRScanner = true
                } label: {
                    Label(L("profiles.scanQr"), systemImage: "qrcode.viewfinder")
                }
            }
        }
    }

    private var profileList: some View {
        List {
            ForEach(store.profiles) { profile in
                ProfileRow(
                    profile: profile,
                    isConnected: vpnManager.connectedProfileID == profile.id && vpnManager.isConnected,
                    onConnect: { connectToProfile(profile) },
                    onTap: { selectedProfile = profile }
                )
            }
            .onDelete(perform: deleteProfiles)
        }
    }

    private func connectToProfile(_ profile: Profile) {
        Task {
            do {
                try await vpnManager.connect(profile: profile)
            } catch {
                connectError = error.localizedDescription
                showingError = true
            }
        }
    }

    private func deleteProfiles(at offsets: IndexSet) {
        for index in offsets {
            store.delete(store.profiles[index])
        }
    }

    private func handleFileImport(_ result: Result<[URL], Error>) {
        switch result {
        case .success(let urls):
            guard let url = urls.first else { return }
            guard url.startAccessingSecurityScopedResource() else {
                importError = "Cannot access file"
                showingImportError = true
                return
            }
            defer { url.stopAccessingSecurityScopedResource() }

            do {
                let data = try Data(contentsOf: url)
                guard let encrypted = String(data: data, encoding: .utf8) else {
                    importError = "File is not valid text"
                    showingImportError = true
                    return
                }

                var error: NSError?
                let json = MobileDecryptProfile(encrypted.trimmingCharacters(in: .whitespacesAndNewlines), &error)
                if let error {
                    importError = error.localizedDescription
                    showingImportError = true
                    return
                }

                guard let jsonData = json.data(using: .utf8),
                      var profile = try? JSONDecoder().decode(Profile.self, from: jsonData) else {
                    importError = "Invalid profile data"
                    showingImportError = true
                    return
                }

                profile.id = UUID()
                profile.imported = true
                store.save(profile)
            } catch {
                importError = error.localizedDescription
                showingImportError = true
            }

        case .failure(let error):
            importError = error.localizedDescription
            showingImportError = true
        }
    }
}

private struct ProfileRow: View {
    let profile: Profile
    let isConnected: Bool
    let onConnect: () -> Void
    let onTap: () -> Void

    var body: some View {
        Button(action: onTap) {
            HStack(spacing: 12) {
                ZStack {
                    Circle()
                        .fill(isConnected ? Color.green : Color.accentColor)
                        .frame(width: 40, height: 40)
                    Text(profile.avatarLetter)
                        .font(.headline)
                        .foregroundStyle(.white)
                }

                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(profile.displayName)
                            .font(.body)
                            .foregroundStyle(.primary)
                        if profile.imported {
                            Text(L("profile.imported"))
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                    }
                    Text(profile.remote)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Spacer()

                if isConnected {
                    Image(systemName: "checkmark.circle.fill")
                        .foregroundStyle(.green)
                } else {
                    Button {
                        onConnect()
                    } label: {
                        Image(systemName: "play.circle.fill")
                            .font(.title2)
                            .foregroundStyle(Color.accentColor)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.vertical, 4)
        }
        .buttonStyle(.plain)
    }
}
