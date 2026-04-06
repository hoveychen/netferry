import Foundation
import SwiftUI
import Observation

/// Manages the app's language override. When set to a specific language,
/// all `Text(String(localized:bundle:))` calls use the overridden bundle.
/// When set to "system", the default system locale is used.
@Observable
final class LanguageManager: @unchecked Sendable {
    nonisolated(unsafe) static let shared = LanguageManager()

    /// "system", "en", or "zh-Hans"
    var language: String {
        didSet {
            UserDefaults.standard.set(language, forKey: "appLanguage")
            updateBundle()
        }
    }

    /// The bundle to use for localized strings. nil = default (system).
    private(set) var bundle: Bundle?

    private init() {
        self.language = UserDefaults.standard.string(forKey: "appLanguage") ?? "system"
        self.bundle = nil
        updateBundle()
    }

    private func updateBundle() {
        if language == "system" {
            bundle = nil
        } else if let path = Bundle.main.path(forResource: language, ofType: "lproj"),
                  let b = Bundle(path: path) {
            bundle = b
        } else {
            bundle = nil
        }
    }

    /// Convenience to get a localized string respecting the override.
    func localized(_ key: String) -> String {
        if let bundle {
            return NSLocalizedString(key, bundle: bundle, comment: "")
        }
        return NSLocalizedString(key, comment: "")
    }
}

/// A SwiftUI `Text` initializer that uses the language manager's bundle.
extension Text {
    init(l10n key: String) {
        let manager = LanguageManager.shared
        if let bundle = manager.bundle {
            self.init(String(localized: String.LocalizationValue(key), bundle: bundle))
        } else {
            // Use default localization
            self.init(String(localized: String.LocalizationValue(key)))
        }
    }
}
