/*
 * Copyright © 2017-2023 WireGuard LLC. All Rights Reserved.
 * SPDX-License-Identifier: Apache-2.0
 */
package com.dobby.awg.config

import com.dobby.awg.crypto.Key
import com.dobby.awg.crypto.KeyFormatException
import java.util.Collections
import java.util.Objects
import java.util.Optional
import java.util.function.Supplier

/**
 * Represents the configuration for an AmneziaWG peer (a [Peer] block). Peers must have a public key,
 * and may optionally have several other attributes.
 *
 *
 * Instances of this class are immutable.
 */
class Peer private constructor(builder: Builder) {// The collection is already immutable.
    // Defensively copy to ensure immutability even if the Builder is reused.
    /**
     * Returns the peer's set of allowed IPs.
     *
     * @return the set of allowed IPs
     */
    val allowedIps: Set<InetNetwork> =
        Collections.unmodifiableSet(LinkedHashSet(builder.allowedIps))

    /**
     * Returns the peer's endpoint.
     *
     * @return the endpoint, or `Optional.empty()` if none is configured
     */
    val endpoint: Optional<InetEndpoint>

    /**
     * Returns the peer's persistent keepalive.
     *
     * @return the persistent keepalive, or `Optional.empty()` if none is configured
     */
    val persistentKeepalive: Optional<Int>

    /**
     * Returns the peer's pre-shared key.
     *
     * @return the pre-shared key, or `Optional.empty()` if none is configured
     */
    val preSharedKey: Optional<Key>

    /**
     * Returns the peer's public key.
     *
     * @return the public key
     */
    val publicKey: Key

    init {
        endpoint = builder.endpoint
        persistentKeepalive = builder.persistentKeepalive
        preSharedKey = builder.preSharedKey
        publicKey = builder.publicKey ?: throw RuntimeException("Peers must have a public key")
    }

    override fun equals(obj: Any?): Boolean {
        if (obj !is Peer) return false
        val other = obj
        return allowedIps == other.allowedIps && endpoint == other.endpoint && persistentKeepalive == other.persistentKeepalive && preSharedKey == other.preSharedKey && publicKey == other.publicKey
    }

    override fun hashCode(): Int {
        var hash = 1
        hash = 31 * hash + allowedIps.hashCode()
        hash = 31 * hash + endpoint.hashCode()
        hash = 31 * hash + persistentKeepalive.hashCode()
        hash = 31 * hash + preSharedKey.hashCode()
        hash = 31 * hash + publicKey.hashCode()
        return hash
    }

    /**
     * Converts the `Peer` into a string suitable for debugging purposes. The `Peer` is
     * identified by its public key and (if known) its endpoint.
     *
     * @return a concise single-line identifier for the `Peer`
     */
    override fun toString(): String {
        val sb = StringBuilder("(Peer ")
        sb.append(publicKey.toBase64())
        endpoint.ifPresent { ep: InetEndpoint? -> sb.append(" @").append(ep) }
        sb.append(')')
        return sb.toString()
    }

    /**
     * Converts the `Peer` into a string suitable for inclusion in a `awg-quick`
     * configuration file.
     *
     * @return the `Peer` represented as a series of "Key = Value" lines
     */
    fun toAwgQuickString(): String {
        val sb = StringBuilder()
        if (!allowedIps.isEmpty()) sb.append("AllowedIPs = ")
            .append(Attribute.Companion.join(allowedIps)).append('\n')
        endpoint.ifPresent { ep: InetEndpoint? -> sb.append("Endpoint = ").append(ep).append('\n') }
        persistentKeepalive.ifPresent { pk: Int? ->
            sb.append("PersistentKeepalive = ").append(pk).append('\n')
        }
        preSharedKey.ifPresent { psk: Key ->
            sb.append("PreSharedKey = ").append(psk.toBase64()).append('\n')
        }
        sb.append("PublicKey = ").append(publicKey.toBase64()).append('\n')
        return sb.toString()
    }

    /**
     * Serializes the `Peer` for use with the AmneziaWG cross-platform userspace API. Note
     * that not all attributes are included in this representation.
     *
     * @return the `Peer` represented as a series of "key=value" lines
     */
    fun toAwgUserspaceString(): String {
        val sb = StringBuilder()
        // The order here is important: public_key signifies the beginning of a new peer.
        sb.append("public_key=").append(publicKey.toHex()).append('\n')
        for (allowedIp in allowedIps) sb.append("allowed_ip=").append(allowedIp).append('\n')
        endpoint.flatMap { obj: InetEndpoint -> obj.getResolved() }
            .ifPresent { ep: InetEndpoint? -> sb.append("endpoint=").append(ep).append('\n') }
        persistentKeepalive.ifPresent { pk: Int? ->
            sb.append("persistent_keepalive_interval=").append(pk).append('\n')
        }
        preSharedKey.ifPresent { psk: Key ->
            sb.append("preshared_key=").append(psk.toHex()).append('\n')
        }
        return sb.toString()
    }

    class Builder {
        // Defaults to an empty set.
        val allowedIps: MutableSet<InetNetwork> = LinkedHashSet()

        // Defaults to not present.
        var endpoint: Optional<InetEndpoint> = Optional.empty()

        // Defaults to not present.
        var persistentKeepalive: Optional<Int> = Optional.empty()

        // Defaults to not present.
        var preSharedKey: Optional<Key> = Optional.empty()

        // No default; must be provided before building.
        var publicKey: Key? = null

        fun addAllowedIp(allowedIp: InetNetwork): Builder {
            allowedIps.add(allowedIp)
            return this
        }

        fun addAllowedIps(allowedIps: Collection<InetNetwork>?): Builder {
            this.allowedIps.addAll(allowedIps!!)
            return this
        }

        @Throws(BadConfigException::class)
        fun build(): Peer {
            if (publicKey == null) throw BadConfigException()
            return Peer(this)
        }

        @Throws(BadConfigException::class)
        fun parseAllowedIPs(allowedIps: CharSequence?): Builder {
            try {
                for (allowedIp in Attribute.Companion.split(allowedIps)) addAllowedIp(
                    InetNetwork.Companion.parse(
                        allowedIp
                    )
                )
                return this
            } catch (e: ParseException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parseEndpoint(endpoint: String?): Builder {
            try {
                return setEndpoint(InetEndpoint.Companion.parse(endpoint))
            } catch (e: ParseException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parsePersistentKeepalive(persistentKeepalive: String?): Builder {
            try {
                return setPersistentKeepalive(persistentKeepalive!!.toInt())
            } catch (e: NumberFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parsePreSharedKey(preSharedKey: String?): Builder {
            try {
                return setPreSharedKey(Key.Companion.fromBase64(preSharedKey))
            } catch (e: KeyFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parsePublicKey(publicKey: String?): Builder {
            try {
                return setPublicKey(Key.Companion.fromBase64(publicKey))
            } catch (e: KeyFormatException) {
                throw BadConfigException()
            }
        }

        fun setEndpoint(endpoint: InetEndpoint): Builder {
            this.endpoint = Optional.of(endpoint)
            return this
        }

        @Throws(BadConfigException::class)
        fun setPersistentKeepalive(persistentKeepalive: Int): Builder {
            if (persistentKeepalive < 0 || persistentKeepalive > MAX_PERSISTENT_KEEPALIVE) throw BadConfigException()
            this.persistentKeepalive =
                if (persistentKeepalive == 0) Optional.empty() else Optional.of(persistentKeepalive)
            return this
        }

        fun setPreSharedKey(preSharedKey: Key): Builder {
            this.preSharedKey = Optional.of(preSharedKey)
            return this
        }

        fun setPublicKey(publicKey: Key?): Builder {
            this.publicKey = publicKey
            return this
        }

        companion object {
            // See awg(8)
            private const val MAX_PERSISTENT_KEEPALIVE = 65535
        }
    }

    companion object {
        /**
         * Parses an series of "KEY = VALUE" lines into a `Peer`. Throws [ParseException] if
         * the input is not well-formed or contains unknown attributes.
         *
         * @param lines an iterable sequence of lines, containing at least a public key attribute
         * @return a `Peer` with all of its attributes set from `lines`
         */
        @Throws(BadConfigException::class)
        fun parse(lines: Iterable<CharSequence?>): Peer {
            val builder = Builder()
            for (line in lines) {
                val attribute: Attribute =
                    Attribute.Companion.parse(line).orElseThrow<BadConfigException>(
                        Supplier<BadConfigException> { BadConfigException() })
                when (attribute.key.lowercase()) {
                    "allowedips" -> builder.parseAllowedIPs(attribute.value)
                    "endpoint" -> builder.parseEndpoint(attribute.value)
                    "persistentkeepalive" -> builder.parsePersistentKeepalive(attribute.value)
                    "presharedkey" -> builder.parsePreSharedKey(attribute.value)
                    "publickey" -> builder.parsePublicKey(attribute.value)
                    else -> throw BadConfigException()
                }
            }
            return builder.build()
        }
    }
}
