import DobbyDiagnosticFiles
import Foundation

/// Keeps the NetworkExtension stop callback behind Go-owned generation cleanup.
public enum IOSProviderStopBoundary {
    /// Stops the Go session, retains platform callbacks on cleanup failure,
    /// cancels path monitoring, and completes the OS stop only after those
    /// operations finish.
    ///
    /// Every potentially blocked stage records its begin immediately before the
    /// call and its end after the call returns, so an interrupted teardown names
    /// the stage it was entered in and not only the stages that finished. A nil
    /// `stageLog` keeps the boundary silent for callers without diagnostics.
    @discardableResult
    public static func perform(
        stopGoSessionAndWait: () -> String,
        releasePlatformCallbacks: () -> Void,
        stopPathMonitoring: () -> Void,
        completeOSStop: (_ cleanupFailure: String) -> Void,
        stageLog: URL? = nil,
        attempt: String = DiagnosticsAttempt.current
    ) -> String {
        var recorder = StageRecorder(log: stageLog, attempt: attempt)
        let failure = recorder.run("go-stop") { stopGoSessionAndWait() }
        recorder.finish("go-stop", failure.isEmpty ? "ok" : "failed",
                        extra: failure.isEmpty ? [:] : ["error": failure])
        if failure.isEmpty {
            recorder.run("runtime-release") {
                releasePlatformCallbacks()
                return ""
            }
            recorder.finish("runtime-release", "ok")
        }
        recorder.run("path-monitor-release") {
            stopPathMonitoring()
            return ""
        }
        recorder.finish("path-monitor-release", "ok")
        completeOSStop(failure)
        return failure
    }

    /// Records begin/end pairs for one teardown on one log destination.
    private struct StageRecorder {
        let log: URL?
        let attempt: String
        var started: [String: TimeInterval] = [:]

        init(log: URL?, attempt: String) {
            self.log = log
            self.attempt = attempt
        }

        /// Writes the begin record and runs the stage.
        @discardableResult
        mutating func run(_ stage: String, _ body: () -> String) -> String {
            started[stage] = DiagnosticFiles.monotonicNow()
            write(stage, phase: "begin", result: nil, elapsedMs: nil, extra: [:])
            return body()
        }

        /// Writes the end record of a stage begun by `run`.
        mutating func finish(_ stage: String, _ result: String, extra: [String: Any] = [:]) {
            let startedAt = started.removeValue(forKey: stage)
            let elapsed = startedAt.map { DiagnosticFiles.elapsedMs(since: $0) }
            write(stage, phase: "end", result: result, elapsedMs: elapsed, extra: extra)
        }

        private func write(
            _ stage: String, phase: String, result: String?, elapsedMs: Int?, extra: [String: Any]
        ) {
            guard let log else { return }
            // The Go raw-capture monitor owns rotation of this destination.
            DiagnosticFiles.stage(operation: "os-teardown", stage: stage, role: "ios-tunnel",
                                  phase: phase, result: result, elapsedMs: elapsedMs,
                                  attemptID: attempt, extraFields: extra, to: log, rotating: false)
        }
    }
}
