package interop.exceptions

class VpnServiceStatusException(reason: Exception) : Exception(reason)
class VpnServiceInternalException(reason: String) : Exception(reason)
