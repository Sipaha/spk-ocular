param([int]$ProcessId, [string]$OutputPath)
$ErrorActionPreference = 'Stop'
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
$process = Get-Process -Id $ProcessId
$window = $process.MainWindowHandle
if ($window -eq [IntPtr]::Zero) { throw 'The owned application has no native window' }
$owner = [uint32]0
[void][NativeWindow]::GetWindowThreadProcessId($window, [ref]$owner)
if ($owner -ne $ProcessId) { throw 'The native window does not belong to the owned application' }
[void][NativeWindow]::DwmFlush()
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
  $bitmap.Save($OutputPath,[Drawing.Imaging.ImageFormat]::Png)
} finally { $graphics.Dispose(); $bitmap.Dispose() }
