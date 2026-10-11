import Darwin
import Foundation

/// With `-bench`, logs how long after the process started each launch step finished
/// (`BENCH launch <step>: <ms>`). Costs nothing otherwise.
enum LaunchTrace {
    private static let enabled = ProcessInfo.processInfo.arguments.contains("-bench")

    private static let processStart: Date? = {
        var info = kinfo_proc()
        var size = MemoryLayout<kinfo_proc>.stride
        var mib: [Int32] = [CTL_KERN, KERN_PROC, KERN_PROC_PID, getpid()]
        guard sysctl(&mib, UInt32(mib.count), &info, &size, nil, 0) == 0 else { return nil }
        let t = info.kp_proc.p_un.__p_starttime
        return Date(timeIntervalSince1970: TimeInterval(t.tv_sec) + TimeInterval(t.tv_usec) / 1_000_000)
    }()

    static func mark(_ step: String) {
        guard enabled, let start = processStart else { return }
        NSLog("BENCH launch %@: %.0f ms", step, Date().timeIntervalSince(start) * 1000)
    }
}
