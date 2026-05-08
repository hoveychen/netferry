import Foundation

struct TunnelStats: Codable, Equatable {
    // These field names must match the Go statsSnapshot JSON keys exactly.
    var rxBytesPerSec: Int64
    var txBytesPerSec: Int64
    var totalRxBytes: Int64
    var totalTxBytes: Int64
    var activeConns: Int32
    var totalConns: Int64
    var dnsQueries: Int64

    init() {
        rxBytesPerSec = 0
        txBytesPerSec = 0
        totalRxBytes = 0
        totalTxBytes = 0
        activeConns = 0
        totalConns = 0
        dnsQueries = 0
    }

    static func from(json: String) -> TunnelStats? {
        guard let data = json.data(using: .utf8) else { return nil }
        return try? JSONDecoder().decode(TunnelStats.self, from: data)
    }

    var formattedRxSpeed: String { ByteFormatter.speed(rxBytesPerSec) }
    var formattedTxSpeed: String { ByteFormatter.speed(txBytesPerSec) }
    var formattedTotalRx: String { ByteFormatter.bytes(totalRxBytes) }
    var formattedTotalTx: String { ByteFormatter.bytes(totalTxBytes) }
}

enum ByteFormatter {
    private static let kb: Double = 1024
    private static let mb: Double = 1024 * 1024
    private static let gb: Double = 1024 * 1024 * 1024

    static func bytes(_ value: Int64) -> String {
        let v = Double(value)
        if v < kb { return String(format: "%.0f B", v) }
        if v < mb { return String(format: "%.1f KB", v / kb) }
        if v < gb { return String(format: "%.1f MB", v / mb) }
        return String(format: "%.2f GB", v / gb)
    }

    static func speed(_ bytesPerSec: Int64) -> String {
        let v = Double(bytesPerSec)
        if v < kb { return String(format: "%.0f B/s", v) }
        if v < mb { return String(format: "%.1f KB/s", v / kb) }
        return String(format: "%.2f MB/s", v / mb)
    }

    /// Compact form for chart Y-axis labels — e.g. "1.2 KB/s", "5 MB/s".
    static func speedShort(_ bytesPerSec: Double) -> String {
        if bytesPerSec < kb { return String(format: "%.0f B/s", bytesPerSec) }
        if bytesPerSec < mb { return String(format: "%.0f KB/s", bytesPerSec / kb) }
        return String(format: "%.1f MB/s", bytesPerSec / mb)
    }
}
