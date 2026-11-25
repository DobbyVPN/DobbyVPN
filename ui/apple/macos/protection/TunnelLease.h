#ifndef DOBBY_TUNNEL_LEASE_H
#define DOBBY_TUNNEL_LEASE_H

#include <stdbool.h>

int DobbyOpenProtectionLease(bool writer);
bool DobbyReserveTunnelProcess(int fd);
bool DobbyAcquireTunnelLease(int fd);
bool DobbyTunnelOwnsLease(int fd);
bool DobbyMarkFilterReady(int fd);
bool DobbyFilterIsReady(int fd);

#endif
