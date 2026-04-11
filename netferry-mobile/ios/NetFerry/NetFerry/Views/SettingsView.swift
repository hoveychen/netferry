import SwiftUI
import NetFerryEngine

struct SettingsView: View {
    @AppStorage("appTheme") private var appTheme = "system"

    private var langManager: LanguageManager { LanguageManager.shared }

    private let appVersion: String = {
        let version = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "1.0"
        let build = Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "1"
        return "\(version) (\(build))"
    }()

    private let engineVersion: String = MobileGetVersion()

    var body: some View {
        NavigationStack {
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

                // ── ABOUT ───────────────────────────────────────────
                Section {
                    LabeledContent(L("settings.version"), value: appVersion)
                    LabeledContent(L("settings.engineVersion"), value: engineVersion)
                } header: {
                    Text(l10n: "settings.section.about")
                }
            }
            .navigationTitle(Text(l10n: "settings.title"))
            .navigationBarTitleDisplayMode(.inline)
        }
    }
}
