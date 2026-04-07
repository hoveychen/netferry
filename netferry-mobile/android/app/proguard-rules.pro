# Keep Go mobile bindings
-keep class mobile.** { *; }
-keep interface mobile.** { *; }

# Keep Gson serialized classes
-keep class com.netferry.app.model.** { *; }
-keepclassmembers class com.netferry.app.model.** { *; }

# Keep Gson TypeToken generic signatures (required by R8 full mode)
-keep class com.google.gson.reflect.TypeToken { *; }
-keep class * extends com.google.gson.reflect.TypeToken

# Keep VPN service
-keep class com.netferry.app.service.NetFerryVpnService { *; }
