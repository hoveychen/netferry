package com.netferry.app.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import com.netferry.app.model.Profile
import com.netferry.app.store.ProfileStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

class ProfileViewModel(application: Application) : AndroidViewModel(application) {

    private val store = ProfileStore(application)

    private val _profiles = MutableStateFlow<List<Profile>>(emptyList())
    val profiles: StateFlow<List<Profile>> = _profiles.asStateFlow()

    private val _currentProfile = MutableStateFlow<Profile?>(null)
    val currentProfile: StateFlow<Profile?> = _currentProfile.asStateFlow()

    init {
        loadProfiles()
    }

    fun loadProfiles() {
        _profiles.value = store.loadAll()
    }

    fun getProfile(id: String): Profile? {
        return store.getById(id)
    }

    fun loadProfile(id: String) {
        _currentProfile.value = store.getById(id)
    }

    fun newProfile() {
        _currentProfile.value = Profile()
    }

    fun saveProfile(profile: Profile) {
        store.save(profile)
        loadProfiles()
    }

    fun deleteProfile(profileId: String) {
        store.delete(profileId)
        loadProfiles()
        if (_currentProfile.value?.id == profileId) {
            _currentProfile.value = null
        }
    }

    fun getAutoConnectProfileId(): String? {
        return store.getAutoConnectProfileId()
    }

    fun setAutoConnectProfileId(profileId: String?) {
        store.setAutoConnectProfileId(profileId)
    }
}
