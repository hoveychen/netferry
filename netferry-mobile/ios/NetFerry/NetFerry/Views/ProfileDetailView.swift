import SwiftUI

struct ProfileDetailView: View {
    @Environment(ProfileStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    @State private var profile: Profile
    @State private var subnetsText: String
    @State private var excludeSubnetsText: String

    private let isNew: Bool

    init(profile: Profile) {
        let isNew = profile.name.isEmpty && profile.remote.isEmpty
        self.isNew = isNew
        _profile = State(initialValue: profile)
        _subnetsText = State(initialValue: profile.subnets.joined(separator: "\n"))
        _excludeSubnetsText = State(initialValue: profile.excludeSubnets.joined(separator: "\n"))
    }

    var body: some View {
        if profile.imported {
            importedBody
        } else {
            editableBody
        }
    }

    // MARK: - Imported (restricted view)

    private var importedBody: some View {
        Form {
            Section {
                Text(L("profile.imported.notice"))
                    .font(.callout)
                    .foregroundStyle(Color.accentColor)
            }

            Section(L("profile.section.connection")) {
                TextField(L("profile.name"), text: $profile.name)
                    .textContentType(.name)
                    .autocorrectionDisabled()
            }

            Section {
                Button(L("profile.delete.confirm"), role: .destructive) {
                    store.delete(profile)
                    dismiss()
                }
            }
        }
        .navigationTitle(profile.name.isEmpty
            ? L("profile.imported.title")
            : profile.name)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) {
                Button(L("cancel")) { dismiss() }
            }
            ToolbarItem(placement: .confirmationAction) {
                Button(L("save")) {
                    store.save(profile)
                    dismiss()
                }
                .disabled(profile.name.isEmpty)
            }
        }
    }

    // MARK: - Editable (full view)

    private var editableBody: some View {
        Form {
            connectionSection
            jumpHostsSection
            routingSection
            dnsSection
            advancedSection
            notesSection

            if !isNew {
                Section {
                    Button(L("profile.delete.confirm"), role: .destructive) {
                        store.delete(profile)
                        dismiss()
                    }
                }
            }
        }
        .navigationTitle(isNew
            ? L("profile.new")
            : L("profile.edit"))
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) {
                Button(L("cancel")) { dismiss() }
            }
            ToolbarItem(placement: .confirmationAction) {
                Button(L("save")) { saveProfile() }
                    .disabled(profile.remote.isEmpty)
            }
        }
    }

    // MARK: - Connection

    private var connectionSection: some View {
        Section(L("profile.section.connection")) {
            TextField(L("profile.name"), text: $profile.name)
                .textContentType(.name)
                .autocorrectionDisabled()

            TextField(L("profile.remote.hint"), text: $profile.remote)
                .textContentType(.URL)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .keyboardType(.URL)

            VStack(alignment: .leading) {
                Text(l10n: "profile.identityKey")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                TextEditor(text: $profile.identityKey)
                    .font(.system(.caption, design: .monospaced))
                    .frame(minHeight: 120)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            }
        }
    }

    // MARK: - Jump Hosts (each with its own remote + PEM key)

    private var jumpHostsSection: some View {
        Section {
            ForEach(profile.jumpHosts.indices, id: \.self) { index in
                JumpHostEntry(
                    jumpHost: Binding(
                        get: { profile.jumpHosts[index] },
                        set: { profile.jumpHosts[index] = $0 }
                    ),
                    onRemove: { profile.jumpHosts.remove(at: index) }
                )
            }

            Button {
                profile.jumpHosts.append(JumpHost())
            } label: {
                Label(L("profile.jumpHosts.add"), systemImage: "plus.circle")
            }
        } header: {
            Text(l10n: "profile.jumpHosts")
        }
    }

    // MARK: - Routing

    private var routingSection: some View {
        Section(L("profile.section.routing")) {
            Toggle(L("profile.autoNets"), isOn: $profile.autoNets)
            Toggle(L("profile.autoExcludeLan"), isOn: $profile.autoExcludeLan)

            VStack(alignment: .leading) {
                Text(l10n: "profile.subnets")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                TextEditor(text: $subnetsText)
                    .font(.system(.caption, design: .monospaced))
                    .frame(minHeight: 60)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            }

            VStack(alignment: .leading) {
                Text(l10n: "profile.excludeSubnets")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                TextEditor(text: $excludeSubnetsText)
                    .font(.system(.caption, design: .monospaced))
                    .frame(minHeight: 60)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            }
        }
    }

    // MARK: - DNS

    private var dnsSection: some View {
        Section(L("profile.section.dns")) {
            Picker(L("profile.dns.mode"), selection: $profile.dns) {
                Text(l10n: "profile.dns.off").tag("off")
                Text(l10n: "profile.dns.all").tag("all")
                Text(l10n: "profile.dns.specific").tag("specific")
            }
            .pickerStyle(.segmented)

            if profile.dns != "off" {
                TextField(L("profile.dns.target.hint"), text: $profile.dnsTarget)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)
            }
        }
    }

    // MARK: - Advanced

    private var advancedSection: some View {
        Section(L("profile.section.advanced")) {
            Stepper(L("profile.poolSize") + ": \(profile.poolSize)",
                    value: $profile.poolSize, in: 1...10)
            Toggle(L("profile.splitConn"), isOn: $profile.splitConn)
            Picker(L("profile.tcpBalance"), selection: $profile.tcpBalanceMode) {
                Text(l10n: "profile.tcpBalance.leastLoaded").tag("least-loaded")
                Text(l10n: "profile.tcpBalance.roundRobin").tag("round-robin")
            }
            Toggle(L("profile.enableUdp"), isOn: $profile.enableUdp)
            Toggle(L("profile.blockUdp"), isOn: $profile.blockUdp)
            Toggle(L("profile.disableIpv6"), isOn: $profile.disableIpv6)
            Stepper("MTU: \(profile.mtu)", value: $profile.mtu, in: 1280...9000, step: 100)
            TextField(L("profile.extraSsh.hint"), text: $profile.extraSshOptions)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
        }
    }

    // MARK: - Notes

    private var notesSection: some View {
        Section(L("profile.notes")) {
            TextEditor(text: $profile.notes)
                .frame(minHeight: 60)
        }
    }

    // MARK: - Save

    private func saveProfile() {
        profile.subnets = subnetsText
            .split(separator: "\n")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
        profile.excludeSubnets = excludeSubnetsText
            .split(separator: "\n")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }

        if profile.subnets.isEmpty {
            profile.subnets = ["0.0.0.0/0"]
        }

        store.save(profile)
        dismiss()
    }
}

// MARK: - Jump Host Entry (remote + expandable PEM key)

private struct JumpHostEntry: View {
    @Binding var jumpHost: JumpHost
    let onRemove: () -> Void
    @State private var showKey = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                TextField(L("profile.jumpHosts.remote"), text: $jumpHost.remote)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)

                Button(role: .destructive) {
                    onRemove()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
            }

            DisclosureGroup(
                L("profile.jumpHosts.identityKey"),
                isExpanded: $showKey
            ) {
                TextEditor(text: $jumpHost.identityKey)
                    .font(.system(.caption, design: .monospaced))
                    .frame(minHeight: 80)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            }
            .font(.caption)
            .foregroundStyle(.secondary)
        }
    }
}
