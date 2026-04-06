import Foundation
import NetworkExtension
import Observation

@Observable
@MainActor
final class VPNManager {
    private(set) var status: NEVPNStatus = .disconnected
    private(set) var connectedProfileID: UUID?
    private(set) var stats: TunnelStats = TunnelStats()

    private var manager: NETunnelProviderManager?
    // nonisolated(unsafe) allows deinit to access these for cleanup.
    nonisolated(unsafe) private var statusObserver: NSObjectProtocol?
    nonisolated(unsafe) private var statsTimer: Timer?

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

                if vpnStatus == .connected {
                    self.startStatsPolling()
                } else if vpnStatus == .disconnected {
                    self.stopStatsPolling()
                    self.stats = TunnelStats()
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

    enum VPNError: LocalizedError {
        case invalidConfiguration

        var errorDescription: String? {
            switch self {
            case .invalidConfiguration:
                return "Invalid VPN configuration"
            }
        }
    }
}
