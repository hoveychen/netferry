import SwiftUI

@main
struct NetFerryApp: App {
    @State private var profileStore = ProfileStore()
    @State private var vpnManager = VPNManager()
    @AppStorage("appTheme") private var appTheme = "system"

    var body: some Scene {
        WindowGroup {
            TabView {
                HomeView()
                    .tabItem {
                        Label(L("nav.home"), systemImage: "link")
                    }

                ProfileListView()
                    .tabItem {
                        Label(L("nav.profiles"), systemImage: "list.bullet")
                    }

                SettingsView()
                    .tabItem {
                        Label(L("nav.settings"), systemImage: "gear")
                    }
            }
            .environment(profileStore)
            .environment(vpnManager)
            .preferredColorScheme(colorScheme)
        }
    }

    private var colorScheme: ColorScheme? {
        switch appTheme {
        case "light": return .light
        case "dark": return .dark
        default: return nil // system
        }
    }
}
