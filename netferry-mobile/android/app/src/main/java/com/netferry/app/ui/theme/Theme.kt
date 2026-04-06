package com.netferry.app.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

// ── Neutral palette (shadcn-inspired: zinc/slate grays) ──────────────────────

// Light
private val Zinc50  = Color(0xFFFAFAFA)
private val Zinc100 = Color(0xFFF4F4F5)
private val Zinc200 = Color(0xFFE4E4E7)
private val Zinc300 = Color(0xFFD4D4D8)
private val Zinc400 = Color(0xFFA1A1AA)
private val Zinc500 = Color(0xFF71717A)
private val Zinc600 = Color(0xFF52525B)
private val Zinc700 = Color(0xFF3F3F46)
private val Zinc800 = Color(0xFF27272A)
private val Zinc900 = Color(0xFF18181B)
private val Zinc950 = Color(0xFF09090B)

// Accent: a calm blue (not Material purple)
private val Accent     = Color(0xFF2563EB) // blue-600
private val AccentDark = Color(0xFF3B82F6) // blue-500
private val AccentMuted = Color(0xFFDBEAFE) // blue-100

// Status colors
val StatusGreen   = Color(0xFF22C55E) // green-500
val StatusRed     = Color(0xFFEF4444) // red-500
val StatusOrange  = Color(0xFFF59E0B) // amber-500

private val LightScheme = lightColorScheme(
    primary          = Accent,
    onPrimary        = Color.White,
    primaryContainer = AccentMuted,
    onPrimaryContainer = Accent,

    secondary          = Zinc600,
    onSecondary        = Color.White,
    secondaryContainer = Zinc100,
    onSecondaryContainer = Zinc900,

    tertiary          = StatusGreen,
    onTertiary        = Color.White,

    background       = Zinc50,
    onBackground     = Zinc900,

    surface          = Color.White,
    onSurface        = Zinc900,
    surfaceVariant   = Zinc100,
    onSurfaceVariant = Zinc600,

    outline          = Zinc200,
    outlineVariant   = Zinc100,

    error            = StatusRed,
    onError          = Color.White,

    inverseSurface   = Zinc900,
    inverseOnSurface = Zinc50,
)

private val DarkScheme = darkColorScheme(
    primary          = AccentDark,
    onPrimary        = Color.White,
    primaryContainer = Color(0xFF1E3A5F),
    onPrimaryContainer = AccentDark,

    secondary          = Zinc400,
    onSecondary        = Zinc950,
    secondaryContainer = Zinc800,
    onSecondaryContainer = Zinc200,

    tertiary          = StatusGreen,
    onTertiary        = Color.White,

    background       = Zinc950,
    onBackground     = Zinc100,

    surface          = Zinc900,
    onSurface        = Zinc100,
    surfaceVariant   = Zinc800,
    onSurfaceVariant = Zinc400,

    outline          = Zinc700,
    outlineVariant   = Zinc800,

    error            = StatusRed,
    onError          = Color.White,

    inverseSurface   = Zinc100,
    inverseOnSurface = Zinc900,
)

// ── Typography (clean, modern) ───────────────────────────────────────────────

private val AppTypography = Typography(
    headlineLarge = TextStyle(
        fontSize = 28.sp,
        fontWeight = FontWeight.Bold,
        letterSpacing = (-0.5).sp,
        lineHeight = 34.sp,
    ),
    headlineMedium = TextStyle(
        fontSize = 22.sp,
        fontWeight = FontWeight.SemiBold,
        letterSpacing = (-0.3).sp,
        lineHeight = 28.sp,
    ),
    headlineSmall = TextStyle(
        fontSize = 18.sp,
        fontWeight = FontWeight.SemiBold,
        lineHeight = 24.sp,
    ),
    titleLarge = TextStyle(
        fontSize = 16.sp,
        fontWeight = FontWeight.SemiBold,
        lineHeight = 22.sp,
    ),
    titleMedium = TextStyle(
        fontSize = 14.sp,
        fontWeight = FontWeight.Medium,
        lineHeight = 20.sp,
    ),
    bodyLarge = TextStyle(
        fontSize = 15.sp,
        fontWeight = FontWeight.Normal,
        lineHeight = 22.sp,
    ),
    bodyMedium = TextStyle(
        fontSize = 13.sp,
        fontWeight = FontWeight.Normal,
        lineHeight = 18.sp,
    ),
    bodySmall = TextStyle(
        fontSize = 12.sp,
        fontWeight = FontWeight.Normal,
        lineHeight = 16.sp,
        color = Zinc500,
    ),
    labelLarge = TextStyle(
        fontSize = 13.sp,
        fontWeight = FontWeight.Medium,
        letterSpacing = 0.1.sp,
        lineHeight = 18.sp,
    ),
    labelMedium = TextStyle(
        fontSize = 11.sp,
        fontWeight = FontWeight.Medium,
        letterSpacing = 0.2.sp,
        lineHeight = 16.sp,
    ),
)

// ── Theme composable ─────────────────────────────────────────────────────────

@Composable
fun NetFerryTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit
) {
    // No dynamic color — we always use our curated palette for brand consistency.
    val colorScheme = if (darkTheme) DarkScheme else LightScheme

    MaterialTheme(
        colorScheme = colorScheme,
        typography = AppTypography,
        content = content
    )
}
