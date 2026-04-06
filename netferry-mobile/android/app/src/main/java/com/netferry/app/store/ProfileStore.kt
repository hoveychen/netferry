package com.netferry.app.store

import android.content.Context
import android.content.SharedPreferences
import com.google.gson.Gson
import com.google.gson.reflect.TypeToken
import com.netferry.app.model.Profile

class ProfileStore(context: Context) {

    private val prefs: SharedPreferences =
        context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
    private val gson = Gson()

    fun loadAll(): List<Profile> {
        val json = prefs.getString(KEY_PROFILES, null) ?: return emptyList()
        val type = object : TypeToken<List<Profile>>() {}.type
        return try {
            gson.fromJson(json, type) ?: emptyList()
        } catch (e: Exception) {
            emptyList()
        }
    }

    fun save(profile: Profile) {
        val profiles = loadAll().toMutableList()
        val index = profiles.indexOfFirst { it.id == profile.id }
        if (index >= 0) {
            profiles[index] = profile
        } else {
            profiles.add(profile)
        }
        persist(profiles)
    }

    fun delete(profileId: String) {
        val profiles = loadAll().filter { it.id != profileId }
        persist(profiles)
    }

    fun getById(id: String): Profile? {
        return loadAll().find { it.id == id }
    }

    fun getAutoConnectProfileId(): String? {
        return prefs.getString(KEY_AUTO_CONNECT, null)
    }

    fun setAutoConnectProfileId(profileId: String?) {
        prefs.edit().apply {
            if (profileId == null) {
                remove(KEY_AUTO_CONNECT)
            } else {
                putString(KEY_AUTO_CONNECT, profileId)
            }
            apply()
        }
    }

    private fun persist(profiles: List<Profile>) {
        val json = gson.toJson(profiles)
        prefs.edit().putString(KEY_PROFILES, json).apply()
    }

    companion object {
        private const val PREFS_NAME = "netferry_profiles"
        private const val KEY_PROFILES = "profiles"
        private const val KEY_AUTO_CONNECT = "auto_connect_profile_id"
    }
}
