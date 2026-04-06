# Keep Go mobile bindings
-keep class mobile.** { *; }
-keep interface mobile.** { *; }

# Keep Gson serialized classes
-keep class com.netferry.app.model.** { *; }
-keepclassmembers class com.netferry.app.model.** { *; }

# Keep VPN service
-keep class com.netferry.app.service.NetFerryVpnService { *; }
