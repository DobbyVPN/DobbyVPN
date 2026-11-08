package com.dobby.nativebridge;

/** Tracks completion of the two native initialization stages. */
public final class NativeStartup {
    private volatile boolean loggerReady;
    private volatile boolean attached;

    public synchronized void attach(Runnable initializeLogger, Runnable attachBackend) {
        attached = false;
        loggerReady = false;
        initializeLogger.run();
        loggerReady = true;
        attachBackend.run();
        attached = true;
    }

    public boolean isLoggerReady() { return loggerReady; }
    public boolean isAttached() { return attached; }
}
