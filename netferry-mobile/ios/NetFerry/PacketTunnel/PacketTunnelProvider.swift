import NetworkExtension
import NetFerryEngine

class PacketTunnelProvider: NEPacketTunnelProvider {
    private var engine: MobileEngine?
    private var tunnelCallback: TunnelCallback?
    /// Stored for rebuilding network settings on reconnect (port changes).
    fileprivate var configJSON: String?

    override func startTunnel(
        options: [String: NSObject]?,
        completionHandler: @escaping (Error?) -> Void
    ) {
        guard let proto = protocolConfiguration as? NETunnelProviderProtocol,
              let providerConfig = proto.providerConfiguration,
              let configJSON = providerConfig["configJSON"] as? String else {
            completionHandler(TunnelError.missingConfiguration)
            return
        }

        self.configJSON = configJSON

        let callback = TunnelCallback(provider: self)
        self.tunnelCallback = callback
        guard let eng = MobileNewEngine(callback) else {
            completionHandler(TunnelError.engineCreationFailed)
            return
        }
        self.engine = eng

        // Engine.Start blocks until connected or error, so run on a background queue.
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self else { return }

            do {
                // gomobile maps Go's `func (e *Engine) Start(configJSON string) error`
                // to a throwing Swift method: `engine.start(_ configJSON: String) throws`.
                try self.engine?.start(configJSON)
            } catch {
                NSLog("PacketTunnel: engine start error: \(error)")
                completionHandler(error)
                return
            }

            let socksPort = self.engine?.getSOCKSPort() ?? 0
            let dnsPort = self.engine?.getDNSPort() ?? 0

            let settings = self.buildNetworkSettings(
                configJSON: configJSON,
                socksPort: Int(socksPort),
                dnsPort: Int(dnsPort)
            )

            self.setTunnelNetworkSettings(settings) { error in
                if let error {
                    NSLog("PacketTunnel: setTunnelNetworkSettings error: \(error)")
                    self.engine?.stop()
                    completionHandler(error)
                } else {
                    NSLog("PacketTunnel: tunnel started successfully, SOCKS=%d DNS=%d", socksPort, dnsPort)
                    completionHandler(nil)
                }
            }
        }
    }

    override func stopTunnel(
        with reason: NEProviderStopReason,
        completionHandler: @escaping () -> Void
    ) {
        NSLog("PacketTunnel: stopping tunnel, reason: \(reason.rawValue)")
        engine?.stop()
        engine = nil
        completionHandler()
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        guard let message = String(data: messageData, encoding: .utf8) else {
            completionHandler?(nil)
            return
        }

        switch message {
        case "stats":
            let statsJSON = engine?.getStats() ?? "{}"
            completionHandler?(statsJSON.data(using: .utf8))
        case "state":
            let state = engine?.getState() ?? "unknown"
            completionHandler?(state.data(using: .utf8))
        case "deploy":
            if let cb = tunnelCallback {
                let json = """
                {"sent":\(cb.deploySent),"total":\(cb.deployTotal),"reason":"\(cb.deployReason)"}
                """
                completionHandler?(json.data(using: .utf8))
            } else {
                completionHandler?(nil)
            }
        default:
            completionHandler?(nil)
        }
    }

    // MARK: - Network Settings

    fileprivate func buildNetworkSettings(
        configJSON: String,
        socksPort: Int,
        dnsPort: Int
    ) -> NEPacketTunnelNetworkSettings {
        // tunnelRemoteAddress is a display-only field shown in Settings > VPN.
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")

        // Assign a virtual IP to the TUN interface so the system routes traffic through it.
        let ipv4 = NEIPv4Settings(addresses: ["10.0.0.1"], subnetMasks: ["255.255.255.0"])

        var includeRoutes: [NEIPv4Route] = []
        var excludeRoutes: [NEIPv4Route] = []

        if let data = configJSON.data(using: .utf8),
           let config = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {

            if let subnets = config["subnets"] as? [String] {
                for subnet in subnets {
                    if let route = parseIPv4Route(subnet) {
                        includeRoutes.append(route)
                    }
                }
            }

            if let excludes = config["excludeSubnets"] as? [String] {
                for subnet in excludes {
                    if let route = parseIPv4Route(subnet) {
                        excludeRoutes.append(route)
                    }
                }
            }
        }

        if includeRoutes.isEmpty {
            includeRoutes.append(NEIPv4Route.default())
        }

        ipv4.includedRoutes = includeRoutes
        ipv4.excludedRoutes = excludeRoutes
        settings.ipv4Settings = ipv4

        // SOCKS5 proxy handles TCP connections. The SOCKS5 CONNECT command sends
        // domain names, so DNS resolution happens on the remote server — this means
        // we do NOT need to intercept DNS at the TUN level for TCP traffic.
        let proxy = NEProxySettings()
        proxy.autoProxyConfigurationEnabled = true
        proxy.proxyAutoConfigurationJavaScript = """
            function FindProxyForURL(url, host) {
                return "SOCKS 127.0.0.1:\(socksPort)";
            }
            """
        proxy.matchDomains = [""]
        settings.proxySettings = proxy

        // DNS relay: if the Go engine provides a DNS relay port, route DNS there.
        // NEDNSSettings only supports specifying server IPs (always port 53). Since
        // our Go DNS relay binds to a random port on 127.0.0.1, we cannot directly
        // use NEDNSSettings to reach it. However, the SOCKS5 proxy already handles
        // remote DNS resolution for TCP/CONNECT traffic (domain names are sent to
        // the SOCKS proxy, resolved remotely). We set DNS servers to a fake address
        // to prevent local DNS leaks — any direct DNS queries that bypass the proxy
        // will fail rather than leak to the real DNS.
        if dnsPort > 0 {
            let dns = NEDNSSettings(servers: ["10.0.0.1"])
            dns.matchDomains = [""]  // match all domains
            settings.dnsSettings = dns
        }

        settings.mtu = NSNumber(value: 1500)

        return settings
    }

    // MARK: - CIDR Parsing

    private func parseIPv4Route(_ cidr: String) -> NEIPv4Route? {
        let parts = cidr.split(separator: "/")
        guard parts.count == 2,
              let prefixLen = Int(parts[1]),
              prefixLen >= 0, prefixLen <= 32 else {
            return nil
        }

        let address = String(parts[0])
        let mask = prefixLengthToMask(prefixLen)
        return NEIPv4Route(destinationAddress: address, subnetMask: mask)
    }

    private func prefixLengthToMask(_ prefix: Int) -> String {
        guard prefix >= 0, prefix <= 32 else { return "0.0.0.0" }
        let mask: UInt32 = prefix == 0 ? 0 : ~UInt32(0) << (32 - prefix)
        return [
            (mask >> 24) & 0xFF,
            (mask >> 16) & 0xFF,
            (mask >> 8) & 0xFF,
            mask & 0xFF
        ].map { String($0) }.joined(separator: ".")
    }

    // MARK: - Errors

    enum TunnelError: LocalizedError {
        case missingConfiguration
        case engineCreationFailed

        var errorDescription: String? {
            switch self {
            case .missingConfiguration:
                return "Missing tunnel configuration"
            case .engineCreationFailed:
                return "Failed to create NetFerry engine"
            }
        }
    }
}

// MARK: - Platform Callback

class TunnelCallback: NSObject, MobilePlatformCallbackProtocol {
    private weak var provider: PacketTunnelProvider?

    /// Deploy progress state, readable by handleAppMessage.
    private(set) var deploySent: Int64 = 0
    private(set) var deployTotal: Int64 = 0
    private(set) var deployReason: String = ""

    init(provider: PacketTunnelProvider) {
        self.provider = provider
        super.init()
    }

    func protectSocket(_ fd: Int32) -> Bool {
        // iOS does not need socket protection — the Network Extension framework
        // automatically excludes the extension's own traffic from the tunnel.
        return true
    }

    func onStateChange(_ state: String?) {
        guard let state else { return }
        NSLog("PacketTunnel: state changed to \(state)")
        if state == "reconnecting" {
            // Signal the NE framework that we're temporarily reasserting the tunnel.
            // This keeps the VPN "up" from the OS perspective while we reconnect.
            provider?.reasserting = true
        } else if state == "connected" {
            provider?.reasserting = false
        }
    }

    func onLog(_ msg: String?) {
        guard let msg else { return }
        NSLog("PacketTunnel: \(msg)")
    }

    func onStats(_ statsJSON: String?) {
        // Stats are retrieved on demand via handleAppMessage.
    }

    func onDeployProgress(_ sent: Int64, total: Int64) {
        deploySent = sent
        deployTotal = total
    }

    func onDeployReason(_ reason: String?) {
        deployReason = reason ?? ""
    }

    func onPortsChanged(_ socksPort: Int32, dnsPort: Int32) {
        guard let provider = provider else { return }
        NSLog("PacketTunnel: ports changed SOCKS=%d DNS=%d, updating network settings", socksPort, dnsPort)
        let configJSON = provider.configJSON ?? "{}"
        let settings = provider.buildNetworkSettings(
            configJSON: configJSON,
            socksPort: Int(socksPort),
            dnsPort: Int(dnsPort)
        )
        provider.setTunnelNetworkSettings(settings) { error in
            if let error {
                NSLog("PacketTunnel: setTunnelNetworkSettings after reconnect error: \(error)")
            } else {
                NSLog("PacketTunnel: network settings updated after reconnect")
            }
        }
    }
}
