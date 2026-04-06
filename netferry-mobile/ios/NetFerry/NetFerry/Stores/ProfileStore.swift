import Foundation
import Observation

@Observable
final class ProfileStore {
    private(set) var profiles: [Profile] = []

    private let fileManager = FileManager.default
    private var storageURL: URL {
        let containerURL = fileManager.containerURL(
            forSecurityApplicationGroupIdentifier: "group.com.netferry.app"
        ) ?? fileManager.urls(for: .documentDirectory, in: .userDomainMask).first!
        let dir = containerURL.appendingPathComponent("profiles", isDirectory: true)
        if !fileManager.fileExists(atPath: dir.path) {
            try? fileManager.createDirectory(at: dir, withIntermediateDirectories: true)
        }
        return dir
    }

    init() {
        loadProfiles()
    }

    func loadProfiles() {
        guard let files = try? fileManager.contentsOfDirectory(
            at: storageURL,
            includingPropertiesForKeys: nil
        ) else { return }

        profiles = files
            .filter { $0.pathExtension == "json" }
            .compactMap { url -> Profile? in
                guard let data = try? Data(contentsOf: url) else { return nil }
                return try? JSONDecoder().decode(Profile.self, from: data)
            }
            .sorted { $0.name.localizedCompare($1.name) == .orderedAscending }
    }

    func save(_ profile: Profile) {
        guard let data = try? JSONEncoder().encode(profile) else { return }
        let url = storageURL.appendingPathComponent("\(profile.id.uuidString).json")
        try? data.write(to: url, options: .atomic)
        if let index = profiles.firstIndex(where: { $0.id == profile.id }) {
            profiles[index] = profile
        } else {
            profiles.append(profile)
        }
    }

    func delete(_ profile: Profile) {
        let url = storageURL.appendingPathComponent("\(profile.id.uuidString).json")
        try? fileManager.removeItem(at: url)
        profiles.removeAll { $0.id == profile.id }
    }

    func profile(for id: UUID) -> Profile? {
        profiles.first { $0.id == id }
    }
}
