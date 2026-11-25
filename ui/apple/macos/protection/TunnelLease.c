#include "TunnelLease.h"

#include <errno.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>

// Both extensions use the same inode across upgrades. Only root can traverse
// this installer-owned directory. Never create or replace the file here.
int DobbyOpenProtectionLease(bool writer) {
    int dir = open("/Library/Application Support/DobbyVPN/Protection",
                   O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (dir < 0) return -1;
    struct stat st;
    int fd = -1;
    if (fstat(dir, &st) == 0) {
        if (st.st_uid != 0 || (st.st_mode & 077) != 0) errno = EPERM;
        else fd = openat(dir, "tunnel.lock",
                         (writer ? O_RDWR : O_RDONLY) | O_NONBLOCK | O_NOFOLLOW | O_CLOEXEC);
    }
    int error = errno;
    close(dir);
    if (fd < 0) { errno = error; return -1; }
    if (fstat(fd, &st) == 0) {
        if (S_ISREG(st.st_mode) && st.st_uid == 0 && st.st_nlink == 1 && (st.st_mode & 077) == 0) {
            return fd;
        }
        errno = EPERM;
    }
    error = errno;
    close(fd);
    errno = error;
    return -1;
}

// POSIX releases process-owned locks on exit, including SIGKILL. Each
// extension opens the file once and retains that descriptor for its lifetime.
bool DobbyReserveTunnelProcess(int fd) {
    struct flock lock = {.l_type = F_WRLCK, .l_whence = SEEK_SET, .l_start = 2, .l_len = 1};
    return fcntl(fd, F_SETLK, &lock) == 0;
}

bool DobbyAcquireTunnelLease(int fd) {
    struct flock lock = {.l_type = F_WRLCK, .l_whence = SEEK_SET, .l_len = 1};
    return fcntl(fd, F_SETLK, &lock) == 0;
}

bool DobbyReleaseTunnelLease(int fd) {
    struct flock lock = {.l_type = F_UNLCK, .l_whence = SEEK_SET, .l_len = 1};
    return fcntl(fd, F_SETLK, &lock) == 0;
}

bool DobbyTunnelOwnsLease(int fd) {
    struct flock lock = {.l_type = F_RDLCK, .l_whence = SEEK_SET, .l_len = 1};
    return fcntl(fd, F_GETLK, &lock) == 0 && lock.l_type == F_WRLCK;
}

bool DobbyMarkFilterReady(int fd) {
    struct flock lock = {.l_type = F_RDLCK, .l_whence = SEEK_SET, .l_start = 1, .l_len = 1};
    return fcntl(fd, F_SETLK, &lock) == 0;
}

bool DobbyFilterIsReady(int fd) {
    struct flock lock = {.l_type = F_WRLCK, .l_whence = SEEK_SET, .l_start = 1, .l_len = 1};
    return fcntl(fd, F_GETLK, &lock) == 0 && lock.l_type == F_RDLCK;
}
