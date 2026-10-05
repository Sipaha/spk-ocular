#requires -Version 7.0
param([int]$ProcessId, [string]$OutputPath)
$ErrorActionPreference = 'Stop'
$elapsed = [Diagnostics.Stopwatch]::StartNew()
Write-Host "Starting native window capture with PowerShell $($PSVersionTable.PSVersion) ($([Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture))"
Add-Type -AssemblyName System.Drawing
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class NativeWindow {
  [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window, out Rect rect);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr window, IntPtr targetDC, uint flags);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint processId);
  [DllImport("dwmapi.dll")] public static extern int DwmFlush();
}
'@
Write-Host "Capture types loaded at $($elapsed.ElapsedMilliseconds) ms"
$process = Get-Process -Id $ProcessId
$window = $process.MainWindowHandle
if ($window -eq [IntPtr]::Zero) { throw 'The owned application has no native window' }
$owner = [uint32]0
[void][NativeWindow]::GetWindowThreadProcessId($window, [ref]$owner)
if ($owner -ne $ProcessId) { throw 'The native window does not belong to the owned application' }
Write-Host "Owned window found at $($elapsed.ElapsedMilliseconds) ms"
[void][NativeWindow]::DwmFlush()
Write-Host "Desktop composition flushed at $($elapsed.ElapsedMilliseconds) ms"
$rect = New-Object NativeWindow+Rect
if (![NativeWindow]::GetWindowRect($window, [ref]$rect)) { throw 'Cannot read native window bounds' }
$bitmap = New-Object Drawing.Bitmap ($rect.Right-$rect.Left),($rect.Bottom-$rect.Top),([Drawing.Imaging.PixelFormat]::Format32bppRgb)
$graphics = [Drawing.Graphics]::FromImage($bitmap)
try {
  # Capture the owned HWND, including accelerated content. A screen copy can
  # silently capture another window (for example the runner's OOBE privacy UI)
  # or the taskbar over the application, even after SetForegroundWindow.
  $dc = $graphics.GetHdc()
  try {
    if (![NativeWindow]::PrintWindow($window, $dc, 2)) { throw 'Cannot render the owned native window' }
  } finally { $graphics.ReleaseHdc($dc) }
  Write-Host "Owned window rendered at $($elapsed.ElapsedMilliseconds) ms"
  $bitmap.Save($OutputPath,[Drawing.Imaging.ImageFormat]::Png)
  Write-Host "Native screenshot saved at $($elapsed.ElapsedMilliseconds) ms"
} finally { $graphics.Dispose(); $bitmap.Dispose() }
