import SwiftUI
import Charts

struct HomeView: View {
    @Environment(VPNManager.self) private var vpnManager
    @Environment(ProfileStore.self) private var store
    @State private var selectedProfileID: UUID?
    @State private var speedHistory: [SpeedSample] = []
    @State private var showLogs = false

    var body: some View {
        NavigationStack {
            Group {
                switch vpnManager.status {
                case .disconnected, .invalid:
                    disconnectedView
                case .connecting, .reasserting:
                    connectingView
                case .connected:
                    connectedView
                case .disconnecting:
                    connectedView // show stats while disconnecting
                @unknown default:
                    disconnectedView
                }
            }
            .navigationTitle(L("nav.home"))
            .navigationBarTitleDisplayMode(.inline)
            .onChange(of: vpnManager.stats) { _, newStats in
                appendSpeedSample(newStats)
            }
        }
    }

    // MARK: - Disconnected (Hero)

    private var disconnectedView: some View {
        ScrollView {
            VStack(spacing: 0) {
                Spacer().frame(height: 60)

                // Logo
                ZStack {
                    Circle()
                        .fill(Color.accentColor.opacity(0.1))
                        .frame(width: 88, height: 88)
                    Image(systemName: "shield.fill")
                        .font(.system(size: 40))
                        .foregroundStyle(Color.accentColor)
                }

                Spacer().frame(height: 20)

                Text("NetFerry")
                    .font(.largeTitle)
                    .fontWeight(.bold)

                Spacer().frame(height: 6)

                Text(L("home.tagline"))
                    .font(.subheadline)
                    .foregroundStyle(.secondary)

                Spacer().frame(height: 48)

                if store.profiles.isEmpty {
                    // No profiles
                    Text(L("home.noProfiles"))
                        .font(.headline)

                    Text(L("home.noProfiles.desc"))
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 32)
                } else {
                    // Profile picker
                    profilePicker
                        .padding(.horizontal, 24)

                    Spacer().frame(height: 24)

                    // Connect button
                    Button {
                        connectSelected()
                    } label: {
                        Label("Connect", systemImage: "bolt.shield.fill")
                            .font(.headline)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 14)
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(selectedProfile == nil)
                    .padding(.horizontal, 24)
                }

                Spacer().frame(height: 32)
            }
        }
    }

    // MARK: - Connecting

    private var connectingView: some View {
        VStack(spacing: 24) {
            Spacer()

            statusHeader

            ProgressView()
                .scaleEffect(1.5)
                .padding()

            Button {
                vpnManager.disconnect()
            } label: {
                Label(L("connection.disconnect"), systemImage: "stop.circle")
                    .font(.headline)
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 12)
            }
            .buttonStyle(.bordered)
            .tint(.red)
            .padding(.horizontal, 24)

            Spacer()
        }
    }

    // MARK: - Connected

    private var connectedView: some View {
        ScrollView {
            VStack(spacing: 16) {
                statusHeader
                    .padding(.top, 8)

                // Speed cards
                HStack(spacing: 12) {
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

                // Speed chart
                if speedHistory.count >= 2 {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(L("connection.speed"))
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
                        .chartXAxis(.hidden)
                        .chartLegend(.visible)
                        .frame(height: 120)
                    }
                    .padding()
                    .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
                }

                // Stats row
                LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible()), GridItem(.flexible())], spacing: 12) {
                    StatItem(title: L("connection.activeConns"), value: "\(vpnManager.stats.activeConns)")
                    StatItem(title: L("connection.totalConns"), value: "\(vpnManager.stats.totalConns)")
                    StatItem(title: L("connection.dnsQueries"), value: "\(vpnManager.stats.dnsQueries)")
                }
                .padding()
                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))

                // Collapsible logs
                DisclosureGroup(L("home.logs"), isExpanded: $showLogs) {
                    Text("Engine logs appear here when connected.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.vertical, 8)
                }
                .padding()
                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))

                // Disconnect
                Button {
                    vpnManager.disconnect()
                } label: {
                    Label(L("connection.disconnect"), systemImage: "stop.circle.fill")
                        .font(.headline)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 12)
                }
                .buttonStyle(.borderedProminent)
                .tint(.red)
            }
            .padding()
        }
    }

    // MARK: - Shared Components

    private var statusHeader: some View {
        VStack(spacing: 8) {
            ZStack {
                Circle()
                    .fill(statusColor.opacity(0.15))
                    .frame(width: 72, height: 72)
                Circle()
                    .fill(statusColor.opacity(0.3))
                    .frame(width: 52, height: 52)
                Image(systemName: statusIcon)
                    .font(.title2)
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

    private var profilePicker: some View {
        Picker(L("home.selectProfile"), selection: $selectedProfileID) {
            Text(L("home.selectProfile")).tag(nil as UUID?)
            ForEach(store.profiles) { profile in
                Text(profile.displayName).tag(profile.id as UUID?)
            }
        }
        .pickerStyle(.menu)
        .onAppear {
            if selectedProfileID == nil {
                selectedProfileID = store.profiles.first?.id
            }
        }
    }

    private var selectedProfile: Profile? {
        guard let id = selectedProfileID else { return nil }
        return store.profile(for: id)
    }

    private func connectSelected() {
        guard let profile = selectedProfile else { return }
        Task {
            do {
                try await vpnManager.connect(profile: profile)
            } catch {
                // Error handling done by VPNManager status observation
            }
        }
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
