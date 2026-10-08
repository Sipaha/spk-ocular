import Foundation
import CoreGraphics
import Vision
let pid = Int(CommandLine.arguments[1])!
let output = CommandLine.arguments[2]
let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as! [[String: Any]]
let owned = windows.filter { ($0[kCGWindowOwnerPID as String] as? Int) == pid && ($0[kCGWindowLayer as String] as? Int) == 0 }
guard let window = owned.first, let id = window[kCGWindowNumber as String] as? UInt32 else { fatalError("Owned production window not found") }
let capture = Process(); capture.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture"); capture.arguments = ["-x", "-l", String(id), output]
try capture.run(); capture.waitUntilExit(); guard capture.terminationStatus == 0 else { fatalError("Capture failed") }
let request = VNRecognizeTextRequest(); request.recognitionLevel = .accurate; request.recognitionLanguages = ["en-US"]
try VNImageRequestHandler(url: URL(fileURLWithPath: output)).perform([request])
let text = (request.results ?? []).compactMap { $0.topCandidates(1).first?.string }.joined(separator: " ")
let normalized = text.lowercased().filter { $0.isLetter || $0.isNumber }
guard normalized.contains("spkocular") && normalized.contains("synthetic") else { fatalError("Production webview did not render the application: \(text)") }
print("PASS owned macOS production window and actual application UI: \(text)")
