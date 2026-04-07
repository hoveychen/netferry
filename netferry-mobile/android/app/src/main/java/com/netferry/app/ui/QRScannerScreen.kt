package com.netferry.app.ui

import android.Manifest
import android.util.Log
import android.util.Size
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.compose.ui.platform.LocalLifecycleOwner
import com.google.gson.Gson
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.common.InputImage
import com.netferry.app.R
import com.netferry.app.model.Profile
import kotlinx.coroutines.launch
import mobile.Mobile

private const val TAG = "QRScanner"

@Composable
fun QRScannerScreen(
    onProfileImported: (Profile) -> Unit,
    onBack: () -> Unit
) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val scope = rememberCoroutineScope()
    val snackbarHostState = remember { SnackbarHostState() }

    var hasCameraPermission by remember { mutableStateOf(false) }
    var permissionDenied by remember { mutableStateOf(false) }

    // QR chunk collection state
    val collectedChunks = remember { mutableStateMapOf<Int, String>() }
    var expectedTotal by remember { mutableStateOf(0) }
    var statusText by remember { mutableStateOf("") }
    var isProcessing by remember { mutableStateOf(false) }
    var errorMessage by remember { mutableStateOf<String?>(null) }

    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { granted ->
        hasCameraPermission = granted
        permissionDenied = !granted
    }

    LaunchedEffect(Unit) {
        val result = ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA)
        if (result == android.content.pm.PackageManager.PERMISSION_GRANTED) {
            hasCameraPermission = true
        } else {
            permissionLauncher.launch(Manifest.permission.CAMERA)
        }
    }

    // Pull string resources for use inside callbacks
    val qrPointCamera = stringResource(R.string.qr_point_camera)
    val qrImporting = stringResource(R.string.qr_importing)
    val qrInvalidCode = stringResource(R.string.qr_invalid_code)
    val qrSetMismatch = stringResource(R.string.qr_set_mismatch)

    // Initialize status text
    LaunchedEffect(Unit) {
        statusText = qrPointCamera
    }

    Box(modifier = Modifier.fillMaxSize()) {
        when {
            permissionDenied -> {
                // Permission denied state
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .background(MaterialTheme.colorScheme.background)
                        .padding(32.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center
                ) {
                    Text(
                        stringResource(R.string.qr_camera_required_title),
                        style = MaterialTheme.typography.headlineSmall,
                        textAlign = TextAlign.Center,
                        color = MaterialTheme.colorScheme.onBackground
                    )
                    Spacer(modifier = Modifier.height(16.dp))
                    Text(
                        stringResource(R.string.qr_camera_required_desc),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        textAlign = TextAlign.Center
                    )
                    Spacer(modifier = Modifier.height(24.dp))
                    Button(
                        onClick = {
                            permissionLauncher.launch(Manifest.permission.CAMERA)
                        },
                        shape = RoundedCornerShape(8.dp),
                        colors = ButtonDefaults.buttonColors(
                            containerColor = MaterialTheme.colorScheme.primary
                        )
                    ) {
                        Text(stringResource(R.string.qr_grant_permission))
                    }
                }
            }

            hasCameraPermission -> {
                // Camera preview
                val cameraProviderState = remember { mutableStateOf<ProcessCameraProvider?>(null) }

                DisposableEffect(Unit) {
                    val future = ProcessCameraProvider.getInstance(context)
                    future.addListener({
                        cameraProviderState.value = future.get()
                    }, ContextCompat.getMainExecutor(context))

                    onDispose {
                        cameraProviderState.value?.unbindAll()
                    }
                }

                AndroidView(
                    factory = { ctx ->
                        PreviewView(ctx).apply {
                            scaleType = PreviewView.ScaleType.FILL_CENTER
                        }
                    },
                    modifier = Modifier.fillMaxSize(),
                    update = { previewView ->
                        val cameraProvider = cameraProviderState.value ?: return@AndroidView

                        cameraProvider.unbindAll()

                        val preview = Preview.Builder().build().also {
                            it.setSurfaceProvider(previewView.surfaceProvider)
                        }

                        val imageAnalysis = ImageAnalysis.Builder()
                            .setTargetResolution(Size(1280, 720))
                            .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                            .build()

                        val scanner = BarcodeScanning.getClient()

                        imageAnalysis.setAnalyzer(
                            ContextCompat.getMainExecutor(context)
                        ) { imageProxy ->
                            processImage(imageProxy, scanner) { rawValue ->
                                if (isProcessing) return@processImage
                                if (rawValue == null || !rawValue.startsWith("NF:")) return@processImage

                                val parsed = parseChunk(rawValue)
                                if (parsed == null) {
                                    errorMessage = qrInvalidCode
                                    return@processImage
                                }

                                val (index, total, _) = parsed
                                if (expectedTotal == 0) {
                                    expectedTotal = total
                                } else if (total != expectedTotal) {
                                    errorMessage = qrSetMismatch
                                    collectedChunks.clear()
                                    expectedTotal = 0
                                    return@processImage
                                }

                                if (!collectedChunks.containsKey(index)) {
                                    collectedChunks[index] = rawValue
                                    errorMessage = null
                                }

                                statusText = "${collectedChunks.size}/$total"

                                if (collectedChunks.size == total) {
                                    isProcessing = true
                                    statusText = qrImporting

                                    val chunksArray = (1..total).map { i ->
                                        collectedChunks[i] ?: ""
                                    }
                                    val chunksJson = Gson().toJson(chunksArray)

                                    scope.launch {
                                        try {
                                            val profileJson = Mobile.importFromQR(chunksJson)
                                            val profile = Gson().fromJson(profileJson, Profile::class.java).sanitized()
                                            onProfileImported(profile.copy(imported = true))
                                        } catch (e: Exception) {
                                            Log.e(TAG, "Failed to import profile", e)
                                            errorMessage = e.message ?: "Import failed"
                                            isProcessing = false
                                            collectedChunks.clear()
                                            expectedTotal = 0
                                            statusText = qrPointCamera
                                            snackbarHostState.showSnackbar(
                                                "Import failed: ${e.message}"
                                            )
                                        }
                                    }
                                }
                            }
                        }

                        try {
                            cameraProvider.bindToLifecycle(
                                lifecycleOwner,
                                CameraSelector.DEFAULT_BACK_CAMERA,
                                preview,
                                imageAnalysis
                            )
                        } catch (e: Exception) {
                            Log.e(TAG, "Camera bind failed", e)
                        }
                    }
                )

                // Semi-transparent overlay
                Box(
                    modifier = Modifier
                        .fillMaxSize()
                        .background(Color.Black.copy(alpha = 0.4f))
                )

                // QR cutout area hint (centered rounded rect)
                Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center
                ) {
                    Box(
                        modifier = Modifier
                            .size(240.dp)
                            .clip(RoundedCornerShape(20.dp))
                            .background(Color.Transparent)
                            .padding(2.dp)
                    ) {
                        // The actual transparent cutout is implied by the overlay
                        // This serves as a visual boundary indicator
                        Box(
                            modifier = Modifier
                                .fillMaxSize()
                                .clip(RoundedCornerShape(18.dp))
                                .background(Color.White.copy(alpha = 0.08f))
                        )
                    }
                }

                // Close button top-left
                IconButton(
                    onClick = onBack,
                    modifier = Modifier
                        .align(Alignment.TopStart)
                        .padding(top = 48.dp, start = 16.dp)
                        .size(40.dp)
                        .clip(CircleShape)
                        .background(Color.Black.copy(alpha = 0.5f))
                ) {
                    Icon(
                        Icons.Default.Close,
                        contentDescription = stringResource(R.string.action_back),
                        tint = Color.White,
                        modifier = Modifier.size(22.dp)
                    )
                }

                // Bottom overlay: progress + status
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .align(Alignment.BottomCenter)
                        .padding(24.dp),
                    horizontalAlignment = Alignment.CenterHorizontally
                ) {
                    // Progress bar
                    if (expectedTotal > 0 && !isProcessing) {
                        LinearProgressIndicator(
                            progress = { collectedChunks.size.toFloat() / expectedTotal },
                            modifier = Modifier
                                .fillMaxWidth()
                                .height(4.dp)
                                .clip(RoundedCornerShape(2.dp)),
                            color = MaterialTheme.colorScheme.primary,
                            trackColor = Color.White.copy(alpha = 0.3f),
                        )
                        Spacer(modifier = Modifier.height(12.dp))
                    }

                    // Status text
                    Text(
                        text = statusText.ifEmpty { qrPointCamera },
                        style = if (expectedTotal > 0 && !isProcessing)
                            MaterialTheme.typography.headlineSmall
                        else
                            MaterialTheme.typography.bodyLarge,
                        fontWeight = if (expectedTotal > 0) FontWeight.Bold else FontWeight.Normal,
                        color = Color.White,
                        modifier = Modifier
                            .clip(RoundedCornerShape(12.dp))
                            .background(Color.Black.copy(alpha = 0.6f))
                            .padding(horizontal = 24.dp, vertical = 12.dp)
                    )

                    // Error message
                    if (errorMessage != null) {
                        Spacer(modifier = Modifier.height(8.dp))
                        Text(
                            text = errorMessage!!,
                            style = MaterialTheme.typography.bodyMedium,
                            color = Color.White,
                            modifier = Modifier
                                .clip(RoundedCornerShape(12.dp))
                                .background(MaterialTheme.colorScheme.error.copy(alpha = 0.85f))
                                .padding(horizontal = 24.dp, vertical = 12.dp)
                        )
                    }

                    Spacer(modifier = Modifier.height(16.dp))
                }
            }

            else -> {
                // Waiting for permission
                Box(
                    modifier = Modifier
                        .fillMaxSize()
                        .background(MaterialTheme.colorScheme.background),
                    contentAlignment = Alignment.Center
                ) {
                    Text(
                        stringResource(R.string.qr_requesting_permission),
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
            }
        }

        // Snackbar host
        SnackbarHost(
            hostState = snackbarHostState,
            modifier = Modifier.align(Alignment.BottomCenter)
        )
    }
}

/**
 * Parse a NF QR chunk. Returns (index, total, data) or null if invalid.
 */
private fun parseChunk(raw: String): Triple<Int, Int, String>? {
    if (!raw.startsWith("NF:")) return null
    val rest = raw.substring(3)
    val slashIdx = rest.indexOf('/')
    if (slashIdx < 0) return null
    val colonIdx = rest.indexOf(':', slashIdx)
    if (colonIdx < 0) return null

    val index = rest.substring(0, slashIdx).toIntOrNull() ?: return null
    val total = rest.substring(slashIdx + 1, colonIdx).toIntOrNull() ?: return null
    val data = rest.substring(colonIdx + 1)
    return Triple(index, total, data)
}

@androidx.annotation.OptIn(androidx.camera.core.ExperimentalGetImage::class)
private fun processImage(
    imageProxy: ImageProxy,
    scanner: com.google.mlkit.vision.barcode.BarcodeScanner,
    onResult: (String?) -> Unit
) {
    val mediaImage = imageProxy.image
    if (mediaImage == null) {
        imageProxy.close()
        return
    }

    val inputImage = InputImage.fromMediaImage(mediaImage, imageProxy.imageInfo.rotationDegrees)
    scanner.process(inputImage)
        .addOnSuccessListener { barcodes ->
            val qrCode = barcodes.firstOrNull { it.format == Barcode.FORMAT_QR_CODE }
            onResult(qrCode?.rawValue)
        }
        .addOnFailureListener {
            onResult(null)
        }
        .addOnCompleteListener {
            imageProxy.close()
        }
}
