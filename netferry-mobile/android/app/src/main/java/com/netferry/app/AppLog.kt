package com.netferry.app

import android.util.Log
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * App-wide debug log buffer visible in Settings → Debug Logs.
 */
object AppLog {
    private const val MAX_LINES = 200
    private val fmt = SimpleDateFormat("HH:mm:ss.SSS", Locale.US)

    private val _lines = MutableStateFlow<List<String>>(emptyList())
    val lines: StateFlow<List<String>> = _lines.asStateFlow()

    fun d(tag: String, msg: String) {
        Log.d(tag, msg)
        append("$tag: $msg")
    }

    fun w(tag: String, msg: String) {
        Log.w(tag, msg)
        append("W/$tag: $msg")
    }

    fun e(tag: String, msg: String, throwable: Throwable? = null) {
        Log.e(tag, msg, throwable)
        val extra = throwable?.let { " | ${it.message}" } ?: ""
        append("E/$tag: $msg$extra")
    }

    private fun append(line: String) {
        val ts = fmt.format(Date())
        val entry = "[$ts] $line"
        val updated = (_lines.value + entry).let {
            if (it.size > MAX_LINES) it.drop(it.size - MAX_LINES) else it
        }
        _lines.value = updated
    }

    fun clear() {
        _lines.value = emptyList()
    }
}
