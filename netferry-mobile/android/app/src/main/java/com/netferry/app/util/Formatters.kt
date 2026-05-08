package com.netferry.app.util

private const val KB = 1024.0
private const val MB = 1024.0 * 1024
private const val GB = 1024.0 * 1024 * 1024

fun formatBytes(bytes: Long): String = when {
    bytes < 1024 -> "$bytes B"
    bytes < 1024 * 1024 -> "%.1f KB".format(bytes / KB)
    bytes < 1024L * 1024 * 1024 -> "%.1f MB".format(bytes / MB)
    else -> "%.2f GB".format(bytes / GB)
}

fun formatSpeed(bytesPerSec: Long): String = when {
    bytesPerSec < 1024 -> "$bytesPerSec B/s"
    bytesPerSec < 1024 * 1024 -> "%.1f KB/s".format(bytesPerSec / KB)
    bytesPerSec < 1024L * 1024 * 1024 -> "%.1f MB/s".format(bytesPerSec / MB)
    else -> "%.2f GB/s".format(bytesPerSec / GB)
}

/** Compact form for chart Y-axis labels — keeps the unit so users can read scale at a glance. */
fun formatSpeedShort(bytesPerSec: Long): String = when {
    bytesPerSec < 1024 -> "${bytesPerSec} B/s"
    bytesPerSec < 1_048_576 -> "${bytesPerSec / 1024} KB/s"
    bytesPerSec < 1_073_741_824 -> "${bytesPerSec / 1_048_576} MB/s"
    else -> "${bytesPerSec / 1_073_741_824} GB/s"
}
