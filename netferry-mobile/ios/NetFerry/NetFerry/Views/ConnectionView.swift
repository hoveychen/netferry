import SwiftUI
import Charts
import UIKit

struct ConnectionView: View {
    @Environment(VPNManager.self) private var vpnManager
    @Environment(ProfileStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var speedHistory: [SpeedSample] = []
    @State private var showingLogs = false
    @State private var showDisconnectConfirm = false

    var body: some View {
        NavigationStack {
            VStack(spacing: 24) {
                statusHeader
                Spacer().frame(height: 8)
                speedCards
                speedChart
                statsGrid
                Spacer()
                disconnectButton
            }
            .padding()
            .navigationTitle(L("connection.title"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        dismiss()
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundStyle(.secondary)
                    }
                    .accessibilityLabel(L("close"))
                }
            }
            .onChange(of: vpnManager.stats) { _, newStats in
                appendSpeedSample(newStats)
            }
            .onChange(of: vpnManager.status) { _, newValue in
                if newValue == .disconnected {
                    dismiss()
                }
            }
            .confirmationDialog(
                L("connection.disconnect.title"),
                isPresented: $showDisconnectConfirm,
                titleVisibility: .visible
            ) {
                Button(L("connection.disconnect"), role: .destructive) {
                    UIImpactFeedbackGenerator(style: .medium).impactOccurred()
                    vpnManager.disconnect()
                }
                Button(L("cancel"), role: .cancel) {}
            } message: {
                Text(L("connection.disconnect.message"))
            }
        }
    }

    private var statusHeader: some View {
        VStack(spacing: 8) {
            ZStack {
                Circle()
                    .fill(statusColor.opacity(0.15))
                    .frame(width: 80, height: 80)
                Circle()
                    .fill(statusColor.opacity(0.3))
                    .frame(width: 60, height: 60)
                Image(systemName: statusIcon)
                    .font(.title)
                    .foregroundStyle(statusColor)
            }

            Text(vpnManager.statusText)
                .font(.headline)
                .foregroundStyle(statusColor)

            if let profileID = vpnManager.connectedProfileID,
               let profile = store.profile(for: profileID) {
                Text(profile.displayName)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                Text(profile.remote)
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }
        }
    }

    private var statusColor: Color {
        switch vpnManager.status {
        case .connected: return .green
        case .connecting, .reasserting: return .orange
        case .disconnecting: return .red
        default: return .gray
        }
    }

    private var statusIcon: String {
        switch vpnManager.status {
        case .connected: return "lock.shield.fill"
        case .connecting, .reasserting: return "arrow.triangle.2.circlepath"
        case .disconnecting: return "xmark.shield"
        default: return "shield.slash"
        }
    }

    private var speedCards: some View {
        HStack(spacing: 16) {
            SpeedCard(
                title: L("connection.download"),
                speed: vpnManager.stats.formattedRxSpeed,
                total: vpnManager.stats.formattedTotalRx,
                icon: "arrow.down.circle.fill",
                color: .blue
            )
            SpeedCard(
                title: L("connection.upload"),
                speed: vpnManager.stats.formattedTxSpeed,
                total: vpnManager.stats.formattedTotalTx,
                icon: "arrow.up.circle.fill",
                color: .purple
            )
        }
    }

    private var speedChart: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(L("connection.speedHistory"))
                .font(.caption)
                .foregroundStyle(.secondary)

            Chart(speedHistory) { sample in
                LineMark(
                    x: .value("Time", sample.time),
                    y: .value("Speed", sample.rxKBps),
                    series: .value("Direction", "Download")
                )
                .foregroundStyle(.blue)
                .interpolationMethod(.catmullRom)

                LineMark(
                    x: .value("Time", sample.time),
                    y: .value("Speed", sample.txKBps),
                    series: .value("Direction", "Upload")
                )
                .foregroundStyle(.purple)
                .interpolationMethod(.catmullRom)
            }
            .chartYAxis {
                AxisMarks(position: .leading) { value in
                    AxisGridLine()
                    AxisValueLabel {
                        if let kbs = value.as(Double.self) {
                            Text(ByteFormatter.speedShort(kbs * 1024))
                                .font(.caption2.monospacedDigit())
                        }
                    }
                }
            }
            .chartXAxis(.hidden)
            .chartLegend(.visible)
            .frame(height: 120)
        }
        .padding()
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
    }

    private var statsGrid: some View {
        LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible()), GridItem(.flexible())], spacing: 12) {
            StatItem(title: L("connection.activeConns"), value: "\(vpnManager.stats.activeConns)")
            StatItem(title: L("connection.totalConns"), value: "\(vpnManager.stats.totalConns)")
            StatItem(title: L("connection.dnsQueries"), value: "\(vpnManager.stats.dnsQueries)")
        }
        .padding()
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
    }

    private var disconnectButton: some View {
        Button {
            showDisconnectConfirm = true
        } label: {
            Label(L("connection.disconnect"), systemImage: "stop.circle.fill")
                .font(.headline)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 12)
        }
        .buttonStyle(.borderedProminent)
        .tint(.red)
    }

    private func appendSpeedSample(_ stats: TunnelStats) {
        let sample = SpeedSample(
            time: Date(),
            rxKBps: Double(stats.rxBytesPerSec) / 1024,
            txKBps: Double(stats.txBytesPerSec) / 1024
        )
        speedHistory.append(sample)
        if speedHistory.count > 60 {
            speedHistory.removeFirst()
        }
    }
}

struct SpeedSample: Identifiable {
    let id = UUID()
    let time: Date
    let rxKBps: Double
    let txKBps: Double
}

struct SpeedCard: View {
    let title: String
    let speed: String
    let total: String
    let icon: String
    let color: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: icon)
                    .foregroundStyle(color)
                Text(title)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Text(speed)
                .font(.title3.monospacedDigit())
                .fontWeight(.semibold)
            Text(total)
                .font(.caption2)
                .foregroundStyle(.tertiary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding()
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
    }
}

struct StatItem: View {
    let title: String
    let value: String

    var body: some View {
        VStack(spacing: 4) {
            Text(value)
                .font(.title2.monospacedDigit())
                .fontWeight(.semibold)
            Text(title)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }
}
