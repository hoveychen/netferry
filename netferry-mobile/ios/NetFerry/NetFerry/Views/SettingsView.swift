import SwiftUI
import UniformTypeIdentifiers
import NetFerryEngine

struct SettingsView: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(ProfileStore.self) private var store
    @AppStorage("appTheme") private var appTheme = "system"

    private var langManager: LanguageManager { LanguageManager.shared }

    private let appVersion: String = {
        let version = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "1.0"
        let build = Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "1"
        return "\(version) (\(build))"
    }()

    @State private var importError: String?
    @State private var showImportError = false
    @State private var showImportSuccess = false

    var body: some View {
        Form {
            // ── APPEARANCE ──────────────────────────────────────
            Section {
                // Theme
                VStack(alignment: .leading, spacing: 8) {
                    Text(l10n: "settings.theme")
                    Picker("", selection: $appTheme) {
                        Text(l10n: "settings.theme.system").tag("system")
                        Text(l10n: "settings.theme.light").tag("light")
                        Text(l10n: "settings.theme.dark").tag("dark")
                    }
                    .pickerStyle(.segmented)
                }

                // Language
                VStack(alignment: .leading, spacing: 8) {
                    Text(l10n: "settings.language")
                    Picker("", selection: Binding(
                        get: { langManager.language },
                        set: { langManager.language = $0 }
                    )) {
                        Text(l10n: "settings.language.system").tag("system")
                        Text(l10n: "settings.language.english").tag("en")
                        Text(l10n: "settings.language.chinese").tag("zh-Hans")
                    }
                    .pickerStyle(.segmented)
                }
            } header: {
                Text(l10n: "settings.section.appearance")
            }

            // ── IMPORT ──────────────────────────────────────────
            Section {
                Button {
                    importFile()
                } label: {
                    Label {
                        Text(l10n: "profiles.importFile")
                    } icon: {
                        Image(systemName: "doc.badge.plus")
                    }
                }
            } header: {
                Text(l10n: "settings.section.general")
            }

            // ── ABOUT ───────────────────────────────────────────
            Section {
                LabeledContent(String(localized: "settings.version"), value: appVersion)
            } header: {
                Text(l10n: "settings.section.about")
            }
        }
        .navigationTitle(Text(l10n: "settings.title"))
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button {
                    dismiss()
                } label: {
                    Text(l10n: "done")
                }
            }
        }
        .alert("Import Error", isPresented: $showImportError) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(importError ?? "")
        }
        .alert("Success", isPresented: $showImportSuccess) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(l10n: "import.success")
        }
        .fileImporter(
            isPresented: $showFileImporter,
            allowedContentTypes: [.data],
            allowsMultipleSelection: false
        ) { result in
            handleFileImport(result)
        }
    }

    @State private var showFileImporter = false

    private func importFile() {
        showFileImporter = true
    }

    private func handleFileImport(_ result: Result<[URL], Error>) {
        switch result {
        case .success(let urls):
            guard let url = urls.first else { return }
            guard url.startAccessingSecurityScopedResource() else {
                importError = "Cannot access file"
                showImportError = true
                return
            }
            defer { url.stopAccessingSecurityScopedResource() }

            do {
                let data = try Data(contentsOf: url)
                guard let encrypted = String(data: data, encoding: .utf8) else {
                    importError = "File is not valid text"
                    showImportError = true
                    return
                }

                var error: NSError?
                let json = MobileDecryptProfile(encrypted.trimmingCharacters(in: .whitespacesAndNewlines), &error)
                if let error {
                    importError = error.localizedDescription
                    showImportError = true
                    return
                }

                guard let jsonData = json.data(using: .utf8),
                      var profile = try? JSONDecoder().decode(Profile.self, from: jsonData) else {
                    importError = "Invalid profile data"
                    showImportError = true
                    return
                }

                profile.id = UUID()
                store.save(profile)
                showImportSuccess = true
            } catch {
                importError = error.localizedDescription
                showImportError = true
            }

        case .failure(let error):
            importError = error.localizedDescription
            showImportError = true
        }
    }
}
