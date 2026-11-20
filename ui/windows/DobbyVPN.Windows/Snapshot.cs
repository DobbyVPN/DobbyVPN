using System.Text.Json;
using System.Text.Json.Serialization;

namespace DobbyVPN.Windows;

internal sealed class Snapshot : IJsonOnDeserialized
{
    [JsonRequired, JsonPropertyName("protocol_version")] public int ProtocolVersion { get; init; }
    [JsonRequired, JsonPropertyName("service_version")] public string ServiceVersion { get; init; } = "";
    [JsonRequired, JsonPropertyName("session_id")] public string SessionId { get; init; } = "";
    [JsonRequired, JsonPropertyName("sequence")] public long Sequence { get; init; }
    [JsonRequired, JsonPropertyName("generation")] public long Generation { get; init; }
    [JsonRequired, JsonPropertyName("network_generation")] public ulong NetworkGeneration { get; init; }
    [JsonRequired, JsonPropertyName("vpn_active")] public bool VpnActive { get; init; }
    [JsonRequired, JsonPropertyName("can_retry")] public bool CanRetry { get; init; }
    [JsonRequired, JsonPropertyName("state")] public string State { get; init; } = "IDLE";
    [JsonRequired, JsonPropertyName("primary_action")] public string PrimaryAction { get; init; } = "NONE";
    [JsonRequired, JsonPropertyName("logs_upload_available")] public bool LogsUploadAvailable { get; init; }
    [JsonRequired, JsonPropertyName("source_kind")] public string SourceKind { get; init; } = "";
    [JsonRequired, JsonPropertyName("configured")] public bool Configured { get; set; }
    [JsonPropertyName("source_url")] public string SourceUrl { get; set; } = "";
    [JsonPropertyName("offline_accepted_at")] public string OfflineAcceptedAt { get; init; } = "";
    [JsonPropertyName("source_error")] public string SourceError { get; init; } = "";
    [JsonPropertyName("active_profile")] public Profile? ActiveProfile { get; init; }
    [JsonPropertyName("last_failure")] public Failure? LastFailure { get; init; }
    [JsonRequired, JsonPropertyName("cleanup_complete")] public bool CleanupComplete { get; init; }
    [JsonRequired, JsonPropertyName("recovering")] public bool Recovering { get; init; }
    [JsonPropertyName("service_restore_pending")] public bool ServiceRestorePending { get; init; }
    [JsonRequired, JsonPropertyName("digest")] public string Digest { get; set; } = "";
    [JsonRequired, JsonPropertyName("profiles")] public Profile[] Profiles { get; set; } = [];
    [JsonRequired, JsonPropertyName("active_digest")] public string ActiveDigest { get; init; } = "";
    [JsonRequired, JsonPropertyName("active_mode")] public string ActiveMode { get; init; } = "";
    [JsonRequired, JsonPropertyName("active_index")] public int ActiveIndex { get; init; }
    [JsonPropertyName("pending_target")] public Selection? PendingTarget { get; init; }
    [JsonRequired, JsonPropertyName("can_switch")] public bool CanSwitch { get; init; }

    void IJsonOnDeserialized.OnDeserialized()
    {
        if (Sequence < 0 || Generation < 0)
            throw new JsonException("Negative snapshot generation or sequence.");
        if (Profiles.Any(profile => profile is null))
            throw new JsonException("Snapshot profiles must contain profile objects.");
    }

    internal static Snapshot Decode(JsonElement payload)
    {
        int version = payload.TryGetProperty("protocol_version", out var field) ? field.GetInt32() : 0;
        SessionProtocol.RequireCompatible(version);
        return payload.Deserialize<Snapshot>(Options) ?? throw new JsonException("Missing snapshot.");
    }

    private static readonly JsonSerializerOptions Options = new() { RespectNullableAnnotations = true };
}

internal sealed class Selection
{
    [JsonRequired, JsonPropertyName("digest")] public string Digest { get; init; } = "";
    [JsonRequired, JsonPropertyName("mode")] public string Mode { get; init; } = "";
    [JsonRequired, JsonPropertyName("index")] public int Index { get; init; }
}

internal sealed class Profile
{
    [JsonRequired, JsonPropertyName("index")] public int Index { get; init; }
    [JsonRequired, JsonPropertyName("protocol")] public string Protocol { get; init; } = "";
    [JsonRequired, JsonPropertyName("description")] public string Description { get; init; } = "";
}

internal sealed class Failure
{
    [JsonRequired, JsonPropertyName("code")] public string Code { get; init; } = "";
    [JsonRequired, JsonPropertyName("message")] public string Message { get; init; } = "";
}
