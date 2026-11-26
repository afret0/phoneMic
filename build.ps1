$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
Write-Host 'recv, 一键构建+部署开始'

$goDir = Join-Path $env:USERPROFILE 'tools\go\bin'
$mingwDir = Join-Path $env:USERPROFILE 'tools\mingw64\bin'
$env:JAVA_HOME = Join-Path $env:LOCALAPPDATA 'Programs\jdk-21.0.13.1+2'
$env:ANDROID_HOME = Join-Path $env:LOCALAPPDATA 'Android\Sdk'
$adb = Join-Path $env:ANDROID_HOME 'platform-tools\adb.exe'
$env:PATH = "$goDir;$mingwDir;$env:PATH"
$env:CGO_ENABLED = '1'

$gradlePath = Join-Path $PSScriptRoot 'android\app\build.gradle.kts'
$gradleText = [System.IO.File]::ReadAllText($gradlePath)
if ($gradleText -notmatch 'versionName\s*=\s*"(\d+)\.(\d+)\.(\d+)"') { throw 'fail, build.gradle.kts 未找到 versionName' }
$oldVer = "$($Matches[1]).$($Matches[2]).$($Matches[3])"
$ver = "$($Matches[1]).$($Matches[2]).$([int]$Matches[3] + 1)"
if ($gradleText -notmatch 'versionCode\s*=\s*(\d+)') { throw 'fail, build.gradle.kts 未找到 versionCode' }
$code = [int]$Matches[1] + 1
$gradleText = $gradleText -replace 'versionName\s*=\s*"\d+\.\d+\.\d+"', ('versionName = "' + $ver + '"')
$gradleText = $gradleText -replace 'versionCode\s*=\s*\d+', ('versionCode = ' + $code)
[System.IO.File]::WriteAllText($gradlePath, $gradleText)
$verGoPath = Join-Path $PSScriptRoot 'internal\version\version.go'
$verGoText = [System.IO.File]::ReadAllText($verGoPath)
if ($verGoText -notmatch 'const Version = "\d+\.\d+\.\d+"') { throw 'fail, version.go 未找到版本常量' }
$verGoText = $verGoText -replace 'const Version = "\d+\.\d+\.\d+"', ('const Version = "' + $ver + '"')
[System.IO.File]::WriteAllText($verGoPath, $verGoText)
Write-Host "done, 版本 bump: $oldVer -> $ver, versionCode: $code"

Write-Host 'recv, 开始构建 Android APK'
$gradleBat = Join-Path $env:LOCALAPPDATA 'Programs\gradle-8.14.3\bin\gradle.bat'
Push-Location (Join-Path $PSScriptRoot 'android')
try {
    if (Test-Path $gradleBat) { & $gradleBat :app:assembleDebug --console=plain }
    else { & .\gradlew.bat :app:assembleDebug --console=plain }
    if ($LASTEXITCODE -ne 0) { throw "fail, gradle assembleDebug exit: $LASTEXITCODE" }
} finally { Pop-Location }
$apkSrc = Join-Path $PSScriptRoot 'android\app\build\outputs\apk\debug\app-debug.apk'
Write-Host 'done, APK 构建完成'

Write-Host 'recv, 开始构建桌面 exe'
& go build -o phone_mic.new.exe ./cmd/phone_mic
if ($LASTEXITCODE -ne 0) { throw "fail, go build exit: $LASTEXITCODE" }
Write-Host 'done, exe 构建完成'

$downloads = Join-Path $env:USERPROFILE 'Downloads'
Copy-Item $apkSrc (Join-Path $downloads "phoneMic_${ver}_android_debug.apk") -Force
Copy-Item (Join-Path $PSScriptRoot 'phone_mic.new.exe') (Join-Path $downloads "phoneMic_${ver}_win_x64.exe") -Force
Write-Host "done, 产物已拷 Downloads: phoneMic_${ver}_android_debug.apk / phoneMic_${ver}_win_x64.exe"

$exePath = Join-Path $PSScriptRoot 'phone_mic.exe'
$oldProcs = Get-Process phone_mic -ErrorAction SilentlyContinue
foreach ($p in $oldProcs) {
    Write-Host "hit, 停止旧桌面进程, pid: $($p.Id)"
    Stop-Process -Id $p.Id -Force
    $p.WaitForExit(5000) | Out-Null
}
Move-Item (Join-Path $PSScriptRoot 'phone_mic.new.exe') $exePath -Force
Start-Process -FilePath $exePath -WorkingDirectory $PSScriptRoot
Write-Host 'done, 桌面 app 已重启为新版本'

$targetModel = '2211133C'
if (-not (Test-Path $adb)) {
    Write-Host 'skip, adb 不存在, 跳过手机安装'
} else {
    $serials = & $adb devices | Select-Object -Skip 1 | ForEach-Object { if ($_ -match '^(\S+)\s+device\s*$') { $Matches[1] } }
    $target = $null
    foreach ($s in $serials) {
        $model = (& $adb -s $s shell getprop ro.product.model).Trim()
        if ($model -eq $targetModel) { $target = $s; break }
    }
    if (-not $target) {
        Write-Host "skip, 小米13 ($targetModel) 不在线, 跳过手机安装"
    } else {
        Write-Host "recv, 安装 APK 到小米13, serial: $target"
        & $adb -s $target install -r $apkSrc
        if ($LASTEXITCODE -ne 0) { throw "fail, adb install exit: $LASTEXITCODE" }
        Write-Host 'done, 小米13 安装完成'
    }
}
Write-Host "done, 构建+部署完成, version: $ver"
