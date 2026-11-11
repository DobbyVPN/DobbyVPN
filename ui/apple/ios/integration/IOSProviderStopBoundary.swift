import Foundation

/// Keeps the NetworkExtension stop callback behind Go-owned generation cleanup.
///
/// The boundary owns the *order* of the stop, and reports each stage as it is
/// entered and as it completes. It owns no diagnostic destination itself: the
/// caller supplies a recorder, so this transport-side type stays independent of
/// whichever log writer its host uses.
public enum IOSProviderStopBoundary {
    /// Receives one stage record: stage name, phase, result, elapsed and extras.
    public typealias StageRecorder = (_ stage: String, _ phase: String, _ result: String?,
                                      _ elapsedMs: Int?, _ extra: [String: Any]) -> Void

    /// Stops the Go session, retains platform callbacks on cleanup failure,
    /// cancels path monitoring, and completes the OS stop only after those
    /// operations finish.
    ///
    /// Every potentially blocked stage reports its begin immediately before the
    /// call and its end after the call returns, so an interrupted teardown names
    /// the stage it was entered in and not only the stages that finished. A nil
    /// recorder keeps the boundary silent.
    @discardableResult
    public static func perform(
        stopGoSessionAndWait: () -> String,
        releasePlatformCallbacks: () -> Void,
        stopPathMonitoring: () -> Void,
        completeOSStop: (_ cleanupFailure: String) -> Void,
        recordStage: StageRecorder? = nil
    ) -> String {
        let clock = Monotonic()
        let failure = clock.run("go-stop", recordStage) { stopGoSessionAndWait() }
        clock.finish("go-stop", failure.isEmpty ? "ok" : "failed", recordStage,
                     extra: failure.isEmpty ? [:] : ["error": failure])
        if failure.isEmpty {
            clock.run("runtime-release", recordStage) {
                releasePlatformCallbacks()
                return ""
            }
            clock.finish("runtime-release", "ok", recordStage)
        }
        clock.run("path-monitor-release", recordStage) {
            stopPathMonitoring()
            return ""
        }
        clock.finish("path-monitor-release", "ok", recordStage)
        completeOSStop(failure)
        return failure
    }

    /// Time spent in each stage, so an end record carries its own duration.
    private final class Monotonic {
        private var started: [String: TimeInterval] = [:]
        private func now() -> TimeInterval { ProcessInfo.processInfo.systemUptime }

        @discardableResult
        func run(_ stage: String, _ record: StageRecorder?, _ body: () -> String) -> String {
            started[stage] = now()
            record?(stage, "begin", nil, nil, [:])
            return body()
        }

        func finish(_ stage: String, _ result: String, _ record: StageRecorder?,
                    extra: [String: Any] = [:]) {
            let begin = started.removeValue(forKey: stage)
            let elapsed = begin.map { Int(((now() - $0) * 1000).rounded()) }
            record?(stage, "end", result, elapsed, extra)
        }
    }
}
