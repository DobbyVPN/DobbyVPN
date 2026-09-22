package com.dobby;

import android.app.Instrumentation;
import android.os.Bundle;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Objects;
import java.util.concurrent.atomic.AtomicLong;

/** Emits the complete original throwable beside AndroidX's shortened summary. */
public final class CompleteThrowableReporter {
    private static final int MAX_CHUNK_BYTES = 16 * 1024;
    private static final AtomicLong NEXT_REPORT_ID = new AtomicLong();

    private CompleteThrowableReporter() {}

    public static void report(Instrumentation instrumentation, Throwable failure) {
        Objects.requireNonNull(instrumentation, "instrumentation");
        Objects.requireNonNull(failure, "failure");
        String report = format(failure);
        List<String> chunks = chunk(report);
        long reportId = NEXT_REPORT_ID.incrementAndGet();
        send(instrumentation, String.format(
                "DOBBY_COMPLETE_THROWABLE_BEGIN id=%d chunks=%d bytes=%d chars=%d\n",
                reportId, chunks.size(),
                report.getBytes(StandardCharsets.UTF_8).length, report.length()));
        for (int index = 0; index < chunks.size(); index++) {
            send(instrumentation, String.format(
                    "DOBBY_COMPLETE_THROWABLE_CHUNK id=%d sequence=%d/%d\n%s",
                    reportId, index + 1, chunks.size(), chunks.get(index)));
        }
        send(instrumentation, String.format(
                "DOBBY_COMPLETE_THROWABLE_END id=%d chunks=%d bytes=%d chars=%d\n",
                reportId, chunks.size(),
                report.getBytes(StandardCharsets.UTF_8).length, report.length()));
    }

    /** Formats every frame, cause, and suppressed throwable without elision. */
    public static String format(Throwable failure) {
        Objects.requireNonNull(failure, "failure");
        StringBuilder output = new StringBuilder();
        appendThrowable(output, failure, "ROOT", "", new IdentityHashMap<Throwable, Boolean>());
        return output.toString();
    }

    private static void appendThrowable(
            StringBuilder output,
            Throwable failure,
            String relation,
            String indent,
            IdentityHashMap<Throwable, Boolean> seen) {
        if (seen.put(failure, Boolean.TRUE) != null) {
            output.append(indent).append(relation)
                    .append(" [CIRCULAR_REFERENCE ")
                    .append(failure.getClass().getName()).append("]\n");
            return;
        }
        output.append(indent).append(relation).append(' ')
                .append(failure.getClass().getName());
        String message = failure.getMessage();
        if (message != null) output.append(": ").append(message);
        output.append('\n');
        for (StackTraceElement frame : failure.getStackTrace()) {
            output.append(indent).append("\tat ").append(frame).append('\n');
        }
        for (Throwable suppressed : failure.getSuppressed()) {
            appendThrowable(output, suppressed, "SUPPRESSED", indent + "\t", seen);
        }
        Throwable cause = failure.getCause();
        if (cause != null) {
            appendThrowable(output, cause, "CAUSED_BY", indent + "\t", seen);
        }
    }

    private static List<String> chunk(String report) {
        List<String> chunks = new ArrayList<>();
        StringBuilder current = new StringBuilder();
        int currentBytes = 0;
        for (int offset = 0; offset < report.length();) {
            int codePoint = report.codePointAt(offset);
            int codePointBytes = new String(Character.toChars(codePoint))
                    .getBytes(StandardCharsets.UTF_8).length;
            if (currentBytes > 0 && currentBytes + codePointBytes > MAX_CHUNK_BYTES) {
                chunks.add(current.toString());
                current.setLength(0);
                currentBytes = 0;
            }
            current.appendCodePoint(codePoint);
            currentBytes += codePointBytes;
            offset += Character.charCount(codePoint);
        }
        if (current.length() > 0 || chunks.isEmpty()) chunks.add(current.toString());
        return chunks;
    }

    private static void send(Instrumentation instrumentation, String value) {
        Bundle status = new Bundle();
        status.putString(Instrumentation.REPORT_KEY_STREAMRESULT, value);
        instrumentation.sendStatus(0, status);
    }
}
