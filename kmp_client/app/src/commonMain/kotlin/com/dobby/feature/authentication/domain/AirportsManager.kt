package com.dobby.feature.authentication.domain

import ck_client.app.generated.resources.Res
import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromByteArray
import kotlinx.serialization.protobuf.ProtoBuf
import kotlinx.serialization.protobuf.ProtoNumber
import org.jetbrains.compose.resources.ExperimentalResourceApi

object AirportsManager {
    @OptIn(ExperimentalSerializationApi::class)
    @Serializable
    data class Airport(
        @ProtoNumber(1)
        val name: String,
        @ProtoNumber(2)
        val latitude_deg: Double,
        @ProtoNumber(3)
        val longitude_deg: Double,
    )

    @OptIn(ExperimentalSerializationApi::class)
    @Serializable
    data class AirportList(
        @ProtoNumber(4)
        val airports: List<Airport>,
    )

    @OptIn(ExperimentalResourceApi::class, ExperimentalSerializationApi::class)
    suspend fun loadAirports(): AirportList {
        val data = Res.readBytes("files/airports")
        val obj = ProtoBuf.decodeFromByteArray<AirportList>(data)
        return obj
    }
}