package com.afret0.phonemic

import android.util.Log
import androidx.annotation.OptIn
import androidx.camera.core.CameraSelector
import androidx.camera.core.ExperimentalGetImage
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.google.mlkit.vision.barcode.BarcodeScanner
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.common.InputImage
import java.util.concurrent.Executor
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

private const val SCAN_TAG = "Scan"
private val ScanFg = Color(0xFFF8FAFC)
private val ScanMuted = Color(0xFF94A3B8)
private val ScanGreen = Color(0xFF22C55E)
private val ScanDanger = Color(0xFFEF4444)

@OptIn(ExperimentalGetImage::class)
private fun analyzeFrame(
    proxy: ImageProxy,
    scanner: BarcodeScanner,
    handled: AtomicBoolean,
    main: Executor,
    onResult: (String) -> Unit,
) {
    val media = proxy.image
    if (media == null || handled.get()) {
        proxy.close()
        return
    }
    val image = InputImage.fromMediaImage(media, proxy.imageInfo.rotationDegrees)
    scanner.process(image)
        .addOnSuccessListener { codes ->
            val raw = codes.firstOrNull { it.format == Barcode.FORMAT_QR_CODE }?.rawValue
            if (raw != null && handled.compareAndSet(false, true)) {
                Log.i(SCAN_TAG, "hit, 扫到二维码, raw: $raw")
                main.execute { onResult(raw) }
            }
        }
        .addOnFailureListener { Log.e(SCAN_TAG, "fail, 识别帧失败, err: ${it.message}") }
        .addOnCompleteListener { proxy.close() }
}

@Composable
fun ScanScreen(onResult: (String) -> Unit, onClose: () -> Unit) {
    val ctx = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val previewView = remember { PreviewView(ctx) }
    var err by remember { mutableStateOf<String?>(null) }

    DisposableEffect(Unit) {
        val executor = Executors.newSingleThreadExecutor()
        val main = ContextCompat.getMainExecutor(ctx)
        val scanner = BarcodeScanning.getClient()
        val handled = AtomicBoolean(false)
        val disposed = AtomicBoolean(false)
        var provider: ProcessCameraProvider? = null
        val future = ProcessCameraProvider.getInstance(ctx)
        future.addListener({
            if (disposed.get()) {
                runCatching { future.get().unbindAll() }
                return@addListener
            }
            runCatching {
                val p = future.get()
                provider = p
                val preview = Preview.Builder().build()
                preview.setSurfaceProvider(previewView.surfaceProvider)
                val analysis = ImageAnalysis.Builder()
                    .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                    .build()
                analysis.setAnalyzer(executor) { proxy -> analyzeFrame(proxy, scanner, handled, main, onResult) }
                p.unbindAll()
                p.bindToLifecycle(lifecycleOwner, CameraSelector.DEFAULT_BACK_CAMERA, preview, analysis)
                Log.i(SCAN_TAG, "done, 扫码相机已启动")
            }.onFailure {
                Log.e(SCAN_TAG, "fail, 扫码相机启动失败, err: ${it.message}")
                err = "相机启动失败: ${it.message}"
            }
        }, main)
        onDispose {
            disposed.set(true)
            provider?.unbindAll()
            executor.shutdown()
            runCatching { scanner.close() }
            Log.i(SCAN_TAG, "done, 扫码页已关闭")
        }
    }

    Box(Modifier.fillMaxSize().background(Color.Black)) {
        AndroidView(factory = { previewView }, modifier = Modifier.fillMaxSize())
        Canvas(Modifier.fillMaxSize()) {
            val w = size.width
            val h = size.height
            val box = minOf(w, h) * 0.68f
            val left = (w - box) / 2f
            val top = h * 0.45f - box / 2f
            val scrim = Color.Black.copy(alpha = 0.55f)
            drawRect(scrim, topLeft = Offset.Zero, size = Size(w, top))
            drawRect(scrim, topLeft = Offset(0f, top + box), size = Size(w, h - top - box))
            drawRect(scrim, topLeft = Offset(0f, top), size = Size(left, box))
            drawRect(scrim, topLeft = Offset(left + box, top), size = Size(w - left - box, box))
            val tick = box * 0.10f
            val sw = 4.dp.toPx()
            val right = left + box
            val bottom = top + box
            drawLine(ScanGreen, Offset(left, top), Offset(left + tick, top), sw, StrokeCap.Round)
            drawLine(ScanGreen, Offset(left, top), Offset(left, top + tick), sw, StrokeCap.Round)
            drawLine(ScanGreen, Offset(right - tick, top), Offset(right, top), sw, StrokeCap.Round)
            drawLine(ScanGreen, Offset(right, top), Offset(right, top + tick), sw, StrokeCap.Round)
            drawLine(ScanGreen, Offset(left, bottom - tick), Offset(left, bottom), sw, StrokeCap.Round)
            drawLine(ScanGreen, Offset(left, bottom), Offset(left + tick, bottom), sw, StrokeCap.Round)
            drawLine(ScanGreen, Offset(right - tick, bottom), Offset(right, bottom), sw, StrokeCap.Round)
            drawLine(ScanGreen, Offset(right, bottom - tick), Offset(right, bottom), sw, StrokeCap.Round)
        }
        Text(
            "对准电脑「手机接入」里的二维码",
            color = ScanFg, fontSize = 14.sp, textAlign = TextAlign.Center,
            modifier = Modifier.align(Alignment.TopCenter).systemBarsPadding().padding(top = 24.dp),
        )
        Text(
            "✕", color = ScanFg, fontSize = 20.sp,
            modifier = Modifier.align(Alignment.TopEnd).systemBarsPadding().padding(12.dp)
                .clip(CircleShape).clickable(onClick = onClose).padding(10.dp),
        )
        Text(
            "扫不到也可以回到首页手动输入电脑 IP",
            color = ScanMuted, fontSize = 12.5.sp,
            modifier = Modifier.align(Alignment.BottomCenter).systemBarsPadding().padding(bottom = 28.dp),
        )
        if (err != null) {
            Text(
                err!!, color = ScanDanger, fontSize = 14.sp, textAlign = TextAlign.Center,
                modifier = Modifier.align(Alignment.Center).padding(horizontal = 32.dp),
            )
        }
    }
}