# Start local ANI three services (gateway/kb-service/rag-engine) for P0 test.
# Run: powershell -ExecutionPolicy Bypass -File repo\scripts\start_local_services.ps1
#
# - Reads middleware config from repo/.env (DB/NATS/Redis/MinIO/Milvus -> remote 10.10.1.66).
# - Overrides ANI_GATEWAY_INTERNAL_URL to http://localhost:8080 (k8s DNS un-resolvable locally).
# - Services run in repo dir via E:\Python311\python.exe / Go binary.
param(
    [switch]$Stop
)

$ErrorActionPreference = "Stop"
$repo = "C:\Users\PC\Desktop\ANI\repo"
$py = "E:\Python311\python.exe"
$logDir = Join-Path $repo ".run"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$pfx = "ani-local"

if ($Stop) {
    Get-Process | Where-Object { $_.Path -like "$repo\bin\*" } | Stop-Process -Force -ErrorAction SilentlyContinue
    Get-ChildItem "$logDir\$pfx-*.pid" -ErrorAction SilentlyContinue | ForEach-Object {
        $p = Get-Content $_.FullName
        Stop-Process -Id $p -Force -ErrorAction SilentlyContinue
        Remove-Item $_.FullName -Force -ErrorAction SilentlyContinue
    }
    Write-Host "stopped local services"
    return
}

# 1. parse repo/.env into process env
$envMap = @{}
Get-Content (Join-Path $repo ".env") | ForEach-Object {
    $line = $_.Trim()
    if ($line -and -not $line.StartsWith("#") -and $line.Contains("=")) {
        $idx = $line.IndexOf("=")
        $k = $line.Substring(0, $idx).Trim()
        $v = $line.Substring($idx + 1).Trim()
        if ($k) { $envMap[$k] = $v }
    }
}
foreach ($k in $envMap.Keys) {
    [Environment]::SetEnvironmentVariable($k, $envMap[$k], "Process")
}

# 2. override key vars
$env:ANI_GATEWAY_INTERNAL_URL = "http://localhost:8080"
$env:KB_SERVICE_GRPC_ADDR    = "localhost:50053"
$env:RAG_ENGINE_URL          = "http://localhost:8001"

# 3. start services
function Start-AniBg([string]$name, [string]$file, [string[]]$procArgs, [string]$workDir) {
    $outLog = Join-Path $logDir "$pfx-$name.out.log"
    $errLog = Join-Path $logDir "$pfx-$name.err.log"
    $pidFile = Join-Path $logDir "$pfx-$name.pid"
    if (Test-Path $pidFile) {
        $old = (Get-Content $pidFile -ErrorAction SilentlyContinue | Select-Object -First 1)
        if ($old -and (Get-Process -Id $old -ErrorAction SilentlyContinue)) {
            Write-Host "[skip] $name already running (PID $old)"
            return
        }
    }
    if ($procArgs -and $procArgs.Count -gt 0) {
        $p = Start-Process -FilePath $file -ArgumentList $procArgs -WorkingDirectory $workDir `
            -PassThru -WindowStyle Hidden `
            -RedirectStandardOutput $outLog -RedirectStandardError $errLog
    } else {
        $p = Start-Process -FilePath $file -WorkingDirectory $workDir `
            -PassThru -WindowStyle Hidden `
            -RedirectStandardOutput $outLog -RedirectStandardError $errLog
    }
    Start-Sleep -Seconds 2
    $p.Id | Out-File -Encoding ascii $pidFile
    Write-Host "[start] $name PID=$($p.Id)  out=$outLog  err=$errLog"
}

Start-AniBg "rag" $py @("ai\rag-engine\main.py") $repo
Start-AniBg "kb" $py @("services\kb-service\main.py") $repo

# gateway 额外对象存储配置 (Core /object/upload 依赖真实 MinIO)
$env:OBJECT_STORE_PROVIDER         = "minio"
$env:OBJECT_STORE_ENDPOINT         = $env:MINIO_ENDPOINT
$env:OBJECT_STORE_PUBLIC_ENDPOINT  = "http://" + $env:MINIO_ENDPOINT
$env:OBJECT_STORE_ACCESS_KEY_ID    = $env:MINIO_ACCESS_KEY
$env:OBJECT_STORE_SECRET_ACCESS_KEY = $env:MINIO_SECRET_KEY
$env:OBJECT_STORE_REGION           = "us-east-1"
$env:OBJECT_STORE_SECURE           = "false"
$env:OBJECT_STORE_BUCKET_PREFIX    = "ani-"

# 向量存储配置 (Core /vector-stores 依赖真实 Milvus)
$env:VECTOR_STORE_PROVIDER         = "milvus"
$env:VECTOR_STORE_ENDPOINT         = $env:MILVUS_ADDR
$env:VECTOR_STORE_COLLECTION_PREFIX = "ani_"

Start-AniBg "gateway" (Join-Path $repo "bin\ani-gateway.exe") @() $repo

Write-Host ""
Write-Host "Started 3 services. Logs in $logDir."
Write-Host "  ani-gateway :8080"
Write-Host "  kb-service  :50053/8002"
Write-Host "  rag-engine  :50052/8001"
