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

    var formattedRxSpeed: String {
        Self.formatSpeed(rxBytesPerSec)
    }

    var formattedTxSpeed: String {
        Self.formatSpeed(txBytesPerSec)
    }

    var formattedTotalRx: String {
        Self.formatBytes(totalRxBytes)
    }

    var formattedTotalTx: String {
        Self.formatBytes(totalTxBytes)
    }

    private static func formatSpeed(_ bytesPerSec: Int64) -> String {
        let value = Double(bytesPerSec)
        if value < 1024 {
            return String(format: "%.0f B/s", value)
        } else if value < 1024 * 1024 {
            return String(format: "%.1f KB/s", value / 1024)
        } else {
            return String(format: "%.2f MB/s", value / (1024 * 1024))
        }
    }

    private static func formatBytes(_ bytes: Int64) -> String {
        let value = Double(bytes)
        if value < 1024 {
            return String(format: "%.0f B", value)
        } else if value < 1024 * 1024 {
            return String(format: "%.1f KB", value / 1024)
        } else if value < 1024 * 1024 * 1024 {
            return String(format: "%.1f MB", value / (1024 * 1024))
        } else {
            return String(format: "%.2f GB", value / (1024 * 1024 * 1024))
        }
    }
}
