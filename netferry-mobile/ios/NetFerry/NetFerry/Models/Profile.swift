import Foundation

struct JumpHost: Codable, Hashable {
    var remote: String
    var identityKey: String

    init(remote: String = "", identityKey: String = "") {
        self.remote = remote
        self.identityKey = identityKey
    }
}

struct Profile: Identifiable, Codable, Hashable {
    var id: UUID
    var name: String
    var remote: String
    var identityKey: String
    var jumpHosts: [JumpHost]
    var subnets: [String]
    var excludeSubnets: [String]
    var autoNets: Bool
    var dns: String                 // "off", "all", "specific"
    var dnsTarget: String
    var enableUdp: Bool
    var blockUdp: Bool
    var poolSize: Int
    var splitConn: Bool
    var tcpBalanceMode: String      // "round-robin" or "least-loaded"
    var latencyBufferSize: Int
    var autoExcludeLan: Bool
    var disableIpv6: Bool
    var extraSshOptions: String
    var notes: String
    var mtu: Int

    init(
        id: UUID = UUID(),
        name: String = "",
        remote: String = "",
        identityKey: String = "",
        jumpHosts: [JumpHost] = [],
        subnets: [String] = ["0.0.0.0/0"],
        excludeSubnets: [String] = [],
        autoNets: Bool = false,
        dns: String = "all",
        dnsTarget: String = "",
        enableUdp: Bool = false,
        blockUdp: Bool = true,
        poolSize: Int = 2,
        splitConn: Bool = false,
        tcpBalanceMode: String = "least-loaded",
        latencyBufferSize: Int = 2097152,
        autoExcludeLan: Bool = true,
        disableIpv6: Bool = false,
        extraSshOptions: String = "",
        notes: String = "",
        mtu: Int = 1500
    ) {
        self.id = id
        self.name = name
        self.remote = remote
        self.identityKey = identityKey
        self.jumpHosts = jumpHosts
        self.subnets = subnets
        self.excludeSubnets = excludeSubnets
        self.autoNets = autoNets
        self.dns = dns
        self.dnsTarget = dnsTarget
        self.enableUdp = enableUdp
        self.blockUdp = blockUdp
        self.poolSize = poolSize
        self.splitConn = splitConn
        self.tcpBalanceMode = tcpBalanceMode
        self.latencyBufferSize = latencyBufferSize
        self.autoExcludeLan = autoExcludeLan
        self.disableIpv6 = disableIpv6
        self.extraSshOptions = extraSshOptions
        self.notes = notes
        self.mtu = mtu
    }

    var displayName: String {
        name.isEmpty ? remote : name
    }

    var avatarLetter: String {
        let source = name.isEmpty ? remote : name
        return String(source.prefix(1)).uppercased()
    }

    func toConfigJSON() -> String {
        let jumpHostDicts = jumpHosts.map { jh -> [String: Any] in
            ["remote": jh.remote, "identityKey": jh.identityKey]
        }
        let config: [String: Any] = [
            "remote": remote,
            "identityKey": identityKey,
            "jumpHosts": jumpHostDicts,
            "subnets": subnets,
            "excludeSubnets": excludeSubnets,
            "autoNets": autoNets,
            "autoExcludeLan": autoExcludeLan,
            "dns": dns,
            "dnsTarget": dnsTarget,
            "enableUdp": enableUdp,
            "blockUdp": blockUdp,
            "poolSize": poolSize,
            "splitConn": splitConn,
            "tcpBalanceMode": tcpBalanceMode,
            "latencyBufferSize": latencyBufferSize,
            "disableIpv6": disableIpv6,
            "extraSshOptions": extraSshOptions,
            "notes": notes,
            "mtu": mtu
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: config),
              let json = String(data: data, encoding: .utf8) else {
            return "{}"
        }
        return json
    }
}
