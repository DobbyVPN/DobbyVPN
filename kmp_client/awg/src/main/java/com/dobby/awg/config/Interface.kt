/*
 * Copyright © 2017-2023 WireGuard LLC. All Rights Reserved.
 * SPDX-License-Identifier: Apache-2.0
 */
package com.dobby.awg.config

import com.dobby.awg.crypto.Key
import com.dobby.awg.crypto.KeyFormatException
import com.dobby.awg.crypto.KeyPair
import java.net.InetAddress
import java.util.Collections
import java.util.List
import java.util.Objects
import java.util.Optional
import java.util.function.Supplier
import java.util.stream.Collectors

/**
 * Represents the configuration for an AmneziaWG interface (an [Interface] block). Interfaces must
 * have a private key (used to initialize a `KeyPair`), and may optionally have several other
 * attributes.
 *
 *
 * Instances of this class are immutable.
 */
class Interface private constructor(builder: Builder) {// The collection is already immutable.
    // Defensively copy to ensure immutability even if the Builder is reused.
    /**
     * Returns the set of IP addresses assigned to the interface.
     *
     * @return a set of [InetNetwork]s
     */
    val addresses: Set<InetNetwork> = Collections.unmodifiableSet(LinkedHashSet(builder.addresses))

    /**
     * Returns the set of DNS servers associated with the interface.
     *
     * @return a set of [InetAddress]es
     */
    val dnsServers: Set<InetAddress> =
        Collections.unmodifiableSet(LinkedHashSet(builder.dnsServers))

    /**
     * Returns the set of DNS search domains associated with the interface.
     *
     * @return a set of strings
     */
    val dnsSearchDomains: Set<String> =
        Collections.unmodifiableSet(LinkedHashSet(builder.dnsSearchDomains))

    /**
     * Returns the set of applications excluded from using the interface.
     *
     * @return a set of package names
     */
    val excludedApplications: Set<String> =
        Collections.unmodifiableSet(LinkedHashSet(builder.excludedApplications))

    /**
     * Returns the set of applications included exclusively for using the interface.
     *
     * @return a set of package names
     */
    val includedApplications: Set<String> =
        Collections.unmodifiableSet(LinkedHashSet(builder.includedApplications))

    /**
     * Returns the public/private key pair used by the interface.
     *
     * @return a key pair
     */
    val keyPair: KeyPair

    /**
     * Returns the UDP port number that the AmneziaWG interface will listen on.
     *
     * @return a UDP port number, or `Optional.empty()` if none is configured
     */
    val listenPort: Optional<Int>

    /**
     * Returns the MTU used for the AmneziaWG interface.
     *
     * @return the MTU, or `Optional.empty()` if none is configured
     */
    val mtu: Optional<Int>

    /**
     * Returns the junkPacketCount used for the AmneziaWG interface.
     *
     * @return the junkPacketCount, or `Optional.empty()` if none is configured
     */
    val junkPacketCount: Optional<Int>

    /**
     * Returns the junkPacketMinSize used for the AmneziaWG interface.
     *
     * @return the junkPacketMinSize, or `Optional.empty()` if none is configured
     */
    val junkPacketMinSize: Optional<Int>

    /**
     * Returns the junkPacketMaxSize used for the AmneziaWG interface.
     *
     * @return the junkPacketMaxSize, or `Optional.empty()` if none is configured
     */
    val junkPacketMaxSize: Optional<Int>

    /**
     * Returns the initPacketJunkSize used for the AmneziaWG interface.
     *
     * @return the initPacketJunkSize, or `Optional.empty()` if none is configured
     */
    val initPacketJunkSize: Optional<Int>

    /**
     * Returns the responsePacketJunkSize used for the AmneziaWG interface.
     *
     * @return the responsePacketJunkSize, or `Optional.empty()` if none is configured
     */
    val responsePacketJunkSize: Optional<Int>

    /**
     * Returns the initPacketMagicHeader used for the AmneziaWG interface.
     *
     * @return the initPacketMagicHeader, or `Optional.empty()` if none is configured
     */
    val initPacketMagicHeader: Optional<Long>

    /**
     * Returns the responsePacketMagicHeader used for the AmneziaWG interface.
     *
     * @return the responsePacketMagicHeader, or `Optional.empty()` if none is configured
     */
    val responsePacketMagicHeader: Optional<Long>

    /**
     * Returns the underloadPacketMagicHeader used for the AmneziaWG interface.
     *
     * @return the underloadPacketMagicHeader, or `Optional.empty()` if none is configured
     */
    val underloadPacketMagicHeader: Optional<Long>

    /**
     * Returns the transportPacketMagicHeader used for the AmneziaWG interface.
     *
     * @return the transportPacketMagicHeader, or `Optional.empty()` if none is configured
     */
    val transportPacketMagicHeader: Optional<Long>

    init {
        keyPair = builder.keyPair ?: throw RuntimeException("Interfaces must have a private key")
        listenPort = builder.listenPort
        mtu = builder.mtu
        junkPacketCount = builder.junkPacketCount
        junkPacketMinSize = builder.junkPacketMinSize
        junkPacketMaxSize = builder.junkPacketMaxSize
        initPacketJunkSize = builder.initPacketJunkSize
        responsePacketJunkSize = builder.responsePacketJunkSize
        initPacketMagicHeader = builder.initPacketMagicHeader
        responsePacketMagicHeader = builder.responsePacketMagicHeader
        underloadPacketMagicHeader = builder.underloadPacketMagicHeader
        transportPacketMagicHeader = builder.transportPacketMagicHeader
    }

    override fun equals(obj: Any?): Boolean {
        if (obj !is Interface) return false
        val other = obj
        return addresses == other.addresses && dnsServers == other.dnsServers && dnsSearchDomains == other.dnsSearchDomains && excludedApplications == other.excludedApplications && includedApplications == other.includedApplications && keyPair == other.keyPair && listenPort == other.listenPort && mtu == other.mtu && junkPacketCount == other.junkPacketCount && junkPacketMinSize == other.junkPacketMinSize && junkPacketMaxSize == other.junkPacketMaxSize && initPacketJunkSize == other.initPacketJunkSize && responsePacketJunkSize == other.responsePacketJunkSize && initPacketMagicHeader == other.initPacketMagicHeader && responsePacketMagicHeader == other.responsePacketMagicHeader && underloadPacketMagicHeader == other.underloadPacketMagicHeader && transportPacketMagicHeader == other.transportPacketMagicHeader
    }


    override fun hashCode(): Int {
        var hash = 1
        hash = 31 * hash + addresses.hashCode()
        hash = 31 * hash + dnsServers.hashCode()
        hash = 31 * hash + excludedApplications.hashCode()
        hash = 31 * hash + includedApplications.hashCode()
        hash = 31 * hash + keyPair.hashCode()
        hash = 31 * hash + listenPort.hashCode()
        hash = 31 * hash + mtu.hashCode()
        hash = 31 * hash + junkPacketCount.hashCode()
        hash = 31 * hash + junkPacketMinSize.hashCode()
        hash = 31 * hash + junkPacketMaxSize.hashCode()
        hash = 31 * hash + initPacketJunkSize.hashCode()
        hash = 31 * hash + responsePacketJunkSize.hashCode()
        hash = 31 * hash + initPacketMagicHeader.hashCode()
        hash = 31 * hash + responsePacketMagicHeader.hashCode()
        hash = 31 * hash + underloadPacketMagicHeader.hashCode()
        hash = 31 * hash + transportPacketMagicHeader.hashCode()
        return hash
    }

    /**
     * Converts the `Interface` into a string suitable for debugging purposes. The `Interface` is identified by its public key and (if set) the port used for its UDP socket.
     *
     * @return A concise single-line identifier for the `Interface`
     */
    override fun toString(): String {
        val sb = StringBuilder("(Interface ")
        sb.append(keyPair.publicKey.toBase64())
        listenPort.ifPresent { lp: Int? -> sb.append(" @").append(lp) }
        sb.append(')')
        return sb.toString()
    }

    /**
     * Converts the `Interface` into a string suitable for inclusion in a `awg-quick`
     * configuration file.
     *
     * @return The `Interface` represented as a series of "Key = Value" lines
     */
    fun toAwgQuickString(): String {
        val sb = StringBuilder()
        if (!addresses.isEmpty()) sb.append("Address = ")
            .append(Attribute.Companion.join(addresses)).append('\n')
        if (!dnsServers.isEmpty()) {
            val dnsServerStrings =
                dnsServers.stream().map { obj: InetAddress? -> obj!!.hostAddress }
                    .collect(Collectors.toList())
            dnsServerStrings.addAll(dnsSearchDomains)
            sb.append("DNS = ").append(Attribute.Companion.join(dnsServerStrings)).append('\n')
        }
        if (!excludedApplications.isEmpty()) sb.append("ExcludedApplications = ")
            .append(Attribute.Companion.join(excludedApplications)).append('\n')
        if (!includedApplications.isEmpty()) sb.append("IncludedApplications = ")
            .append(Attribute.Companion.join(includedApplications)).append('\n')
        listenPort.ifPresent { lp: Int? -> sb.append("ListenPort = ").append(lp).append('\n') }
        mtu.ifPresent { m: Int? -> sb.append("MTU = ").append(m).append('\n') }
        junkPacketCount.ifPresent { jc: Int? -> sb.append("Jc = ").append(jc).append('\n') }
        junkPacketMinSize.ifPresent { jmin: Int? -> sb.append("Jmin = ").append(jmin).append('\n') }
        junkPacketMaxSize.ifPresent { jmax: Int? -> sb.append("Jmax = ").append(jmax).append('\n') }
        initPacketJunkSize.ifPresent { s1: Int? -> sb.append("S1 = ").append(s1).append('\n') }
        responsePacketJunkSize.ifPresent { s2: Int? -> sb.append("S2 = ").append(s2).append('\n') }
        initPacketMagicHeader.ifPresent { h1: Long? -> sb.append("H1 = ").append(h1).append('\n') }
        responsePacketMagicHeader.ifPresent { h2: Long? ->
            sb.append("H2 = ").append(h2).append('\n')
        }
        underloadPacketMagicHeader.ifPresent { h3: Long? ->
            sb.append("H3 = ").append(h3).append('\n')
        }
        transportPacketMagicHeader.ifPresent { h4: Long? ->
            sb.append("H4 = ").append(h4).append('\n')
        }
        sb.append("PrivateKey = ").append(keyPair.privateKey.toBase64()).append('\n')
        return sb.toString()
    }

    /**
     * Serializes the `Interface` for use with the AmneziaWG cross-platform userspace API.
     * Note that not all attributes are included in this representation.
     *
     * @return the `Interface` represented as a series of "KEY=VALUE" lines
     */
    fun toAwgUserspaceString(): String {
        val sb = StringBuilder()
        sb.append("private_key=").append(keyPair.privateKey.toHex()).append('\n')
        listenPort.ifPresent { lp: Int? -> sb.append("listen_port=").append(lp).append('\n') }
        junkPacketCount.ifPresent { jc: Int? -> sb.append("jc=").append(jc).append('\n') }
        junkPacketMinSize.ifPresent { jmin: Int? -> sb.append("jmin=").append(jmin).append('\n') }
        junkPacketMaxSize.ifPresent { jmax: Int? -> sb.append("jmax=").append(jmax).append('\n') }
        initPacketJunkSize.ifPresent { s1: Int? -> sb.append("s1=").append(s1).append('\n') }
        responsePacketJunkSize.ifPresent { s2: Int? -> sb.append("s2=").append(s2).append('\n') }
        initPacketMagicHeader.ifPresent { h1: Long? -> sb.append("h1=").append(h1).append('\n') }
        responsePacketMagicHeader.ifPresent { h2: Long? ->
            sb.append("h2=").append(h2).append('\n')
        }
        underloadPacketMagicHeader.ifPresent { h3: Long? ->
            sb.append("h3=").append(h3).append('\n')
        }
        transportPacketMagicHeader.ifPresent { h4: Long? ->
            sb.append("h4=").append(h4).append('\n')
        }
        return sb.toString()
    }

    class Builder {
        // Defaults to an empty set.
        val addresses: MutableSet<InetNetwork> = LinkedHashSet()

        // Defaults to an empty set.
        val dnsServers: MutableSet<InetAddress> = LinkedHashSet()

        // Defaults to an empty set.
        val dnsSearchDomains: MutableSet<String> = LinkedHashSet()

        // Defaults to an empty set.
        val excludedApplications: MutableSet<String> = LinkedHashSet()

        // Defaults to an empty set.
        val includedApplications: MutableSet<String> = LinkedHashSet()

        // No default; must be provided before building.
        var keyPair: KeyPair? = null

        // Defaults to not present.
        var listenPort: Optional<Int> = Optional.empty()

        // Defaults to not present.
        var mtu: Optional<Int> = Optional.empty()

        // Defaults to not present.
        var junkPacketCount: Optional<Int> = Optional.empty()

        // Defaults to not present.
        var junkPacketMinSize: Optional<Int> = Optional.empty()

        // Defaults to not present.
        var junkPacketMaxSize: Optional<Int> = Optional.empty()

        // Defaults to not present.
        var initPacketJunkSize: Optional<Int> = Optional.empty()

        // Defaults to not present.
        var responsePacketJunkSize: Optional<Int> = Optional.empty()

        // Defaults to not present.
        var initPacketMagicHeader: Optional<Long> = Optional.empty()

        // Defaults to not present.
        var responsePacketMagicHeader: Optional<Long> = Optional.empty()

        // Defaults to not present.
        var underloadPacketMagicHeader: Optional<Long> = Optional.empty()

        // Defaults to not present.
        var transportPacketMagicHeader: Optional<Long> = Optional.empty()


        fun addAddress(address: InetNetwork): Builder {
            addresses.add(address)
            return this
        }

        fun addAddresses(addresses: Collection<InetNetwork>?): Builder {
            this.addresses.addAll(addresses!!)
            return this
        }

        fun addDnsServer(dnsServer: InetAddress): Builder {
            dnsServers.add(dnsServer)
            return this
        }

        fun addDnsServers(dnsServers: Collection<InetAddress>): Builder {
            this.dnsServers.addAll(dnsServers)
            return this
        }

        fun addDnsSearchDomain(dnsSearchDomain: String): Builder {
            dnsSearchDomains.add(dnsSearchDomain)
            return this
        }

        fun addDnsSearchDomains(dnsSearchDomains: Collection<String>?): Builder {
            this.dnsSearchDomains.addAll(dnsSearchDomains!!)
            return this
        }

        @Throws(BadConfigException::class)
        fun build(): Interface {
            if (keyPair == null) throw BadConfigException()
            if (!includedApplications.isEmpty() && !excludedApplications.isEmpty()) throw BadConfigException()
            return Interface(this)
        }

        fun excludeApplication(application: String): Builder {
            excludedApplications.add(application)
            return this
        }

        fun excludeApplications(applications: Collection<String>?): Builder {
            excludedApplications.addAll(applications!!)
            return this
        }

        fun includeApplication(application: String): Builder {
            includedApplications.add(application)
            return this
        }

        fun includeApplications(applications: Collection<String>?): Builder {
            includedApplications.addAll(applications!!)
            return this
        }

        @Throws(BadConfigException::class)
        fun parseAddresses(addresses: CharSequence?): Builder {
            try {
                for (address in Attribute.Companion.split(addresses)) addAddress(
                    InetNetwork.Companion.parse(
                        address
                    )
                )
                return this
            } catch (e: ParseException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parseDnsServers(dnsServers: CharSequence?): Builder {
            try {
                for (dnsServer in Attribute.Companion.split(dnsServers)) {
                    try {
                        addDnsServer(InetAddresses.parse(dnsServer))
                    } catch (e: ParseException) {
                        if (e.parsingClass != InetAddress::class.java || !InetAddresses.isHostname(
                                dnsServer
                            )
                        ) throw e
                        addDnsSearchDomain(dnsServer)
                    }
                }
                return this
            } catch (e: ParseException) {
                throw BadConfigException()
            }
        }

        fun parseExcludedApplications(apps: CharSequence?): Builder {
            return excludeApplications(List.of<String>(*Attribute.Companion.split(apps)))
        }

        fun parseIncludedApplications(apps: CharSequence?): Builder {
            return includeApplications(List.of<String>(*Attribute.Companion.split(apps)))
        }

        @Throws(BadConfigException::class)
        fun parseListenPort(listenPort: String?): Builder {
            try {
                return setListenPort(listenPort!!.toInt())
            } catch (e: NumberFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parseMtu(mtu: String?): Builder {
            try {
                return setMtu(mtu!!.toInt())
            } catch (e: NumberFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parseJunkPacketCount(junkPacketCount: String?): Builder {
            try {
                return setJunkPacketCount(junkPacketCount!!.toInt())
            } catch (e: NumberFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parseJunkPacketMinSize(junkPacketMinSize: String?): Builder {
            try {
                return setJunkPacketMinSize(junkPacketMinSize!!.toInt())
            } catch (e: NumberFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parseJunkPacketMaxSize(junkPacketMaxSize: String?): Builder {
            try {
                return setJunkPacketMaxSize(junkPacketMaxSize!!.toInt())
            } catch (e: NumberFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parseInitPacketJunkSize(initPacketJunkSize: String?): Builder {
            try {
                return setInitPacketJunkSize(initPacketJunkSize!!.toInt())
            } catch (e: NumberFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parseResponsePacketJunkSize(responsePacketJunkSize: String?): Builder {
            try {
                return setResponsePacketJunkSize(responsePacketJunkSize!!.toInt())
            } catch (e: NumberFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parseInitPacketMagicHeader(initPacketMagicHeader: String?): Builder {
            try {
                return setInitPacketMagicHeader(initPacketMagicHeader!!.toLong())
            } catch (e: NumberFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parseResponsePacketMagicHeader(responsePacketMagicHeader: String?): Builder {
            try {
                return setResponsePacketMagicHeader(responsePacketMagicHeader!!.toLong())
            } catch (e: NumberFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parseUnderloadPacketMagicHeader(underloadPacketMagicHeader: String?): Builder {
            try {
                return setUnderloadPacketMagicHeader(underloadPacketMagicHeader!!.toLong())
            } catch (e: NumberFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parseTransportPacketMagicHeader(transportPacketMagicHeader: String?): Builder {
            try {
                return setTransportPacketMagicHeader(transportPacketMagicHeader!!.toLong())
            } catch (e: NumberFormatException) {
                throw BadConfigException()
            }
        }

        @Throws(BadConfigException::class)
        fun parsePrivateKey(privateKey: String?): Builder {
            try {
                return setKeyPair(KeyPair(Key.Companion.fromBase64(privateKey)))
            } catch (e: KeyFormatException) {
                throw BadConfigException()
            }
        }

        fun setKeyPair(keyPair: KeyPair?): Builder {
            this.keyPair = keyPair
            return this
        }

        @Throws(BadConfigException::class)
        fun setListenPort(listenPort: Int): Builder {
            if (listenPort < MIN_UDP_PORT || listenPort > MAX_UDP_PORT) throw BadConfigException()
            this.listenPort = if (listenPort == 0) Optional.empty() else Optional.of(listenPort)
            return this
        }

        @Throws(BadConfigException::class)
        fun setMtu(mtu: Int): Builder {
            if (mtu < 0) throw BadConfigException()
            this.mtu = if (mtu == 0) Optional.empty() else Optional.of(mtu)
            return this
        }

        @Throws(BadConfigException::class)
        fun setJunkPacketCount(junkPacketCount: Int): Builder {
            if (junkPacketCount < 0) throw BadConfigException()
            this.junkPacketCount =
                if (junkPacketCount == 0) Optional.empty() else Optional.of(junkPacketCount)
            return this
        }

        @Throws(BadConfigException::class)
        fun setJunkPacketMinSize(junkPacketMinSize: Int): Builder {
            if (junkPacketMinSize < 0) throw BadConfigException()
            this.junkPacketMinSize =
                if (junkPacketMinSize == 0) Optional.empty() else Optional.of(junkPacketMinSize)
            return this
        }

        @Throws(BadConfigException::class)
        fun setJunkPacketMaxSize(junkPacketMaxSize: Int): Builder {
            if (junkPacketMaxSize < 0) throw BadConfigException()
            this.junkPacketMaxSize =
                if (junkPacketMaxSize == 0) Optional.empty() else Optional.of(junkPacketMaxSize)
            return this
        }

        @Throws(BadConfigException::class)
        fun setInitPacketJunkSize(initPacketJunkSize: Int): Builder {
            if (initPacketJunkSize < 0) throw BadConfigException()
            this.initPacketJunkSize =
                if (initPacketJunkSize == 0) Optional.empty() else Optional.of(initPacketJunkSize)
            return this
        }

        @Throws(BadConfigException::class)
        fun setResponsePacketJunkSize(responsePacketJunkSize: Int): Builder {
            if (responsePacketJunkSize < 0) throw BadConfigException()
            this.responsePacketJunkSize =
                if (responsePacketJunkSize == 0) Optional.empty() else Optional.of(
                    responsePacketJunkSize
                )
            return this
        }

        @Throws(BadConfigException::class)
        fun setInitPacketMagicHeader(initPacketMagicHeader: Long): Builder {
            if (initPacketMagicHeader < 0) throw BadConfigException()
            this.initPacketMagicHeader =
                if (initPacketMagicHeader == 0L) Optional.empty() else Optional.of(
                    initPacketMagicHeader
                )
            return this
        }

        @Throws(BadConfigException::class)
        fun setResponsePacketMagicHeader(responsePacketMagicHeader: Long): Builder {
            if (responsePacketMagicHeader < 0) throw BadConfigException()
            this.responsePacketMagicHeader =
                if (responsePacketMagicHeader == 0L) Optional.empty() else Optional.of(
                    responsePacketMagicHeader
                )
            return this
        }

        @Throws(BadConfigException::class)
        fun setUnderloadPacketMagicHeader(underloadPacketMagicHeader: Long): Builder {
            if (underloadPacketMagicHeader < 0) throw BadConfigException()
            this.underloadPacketMagicHeader =
                if (underloadPacketMagicHeader == 0L) Optional.empty() else Optional.of(
                    underloadPacketMagicHeader
                )
            return this
        }

        @Throws(BadConfigException::class)
        fun setTransportPacketMagicHeader(transportPacketMagicHeader: Long): Builder {
            if (transportPacketMagicHeader < 0) throw BadConfigException()
            this.transportPacketMagicHeader =
                if (transportPacketMagicHeader == 0L) Optional.empty() else Optional.of(
                    transportPacketMagicHeader
                )
            return this
        }
    }

    companion object {
        private const val MAX_UDP_PORT = 65535
        private const val MIN_UDP_PORT = 0

        /**
         * Parses an series of "KEY = VALUE" lines into an `Interface`. Throws
         * [ParseException] if the input is not well-formed or contains unknown attributes.
         *
         * @param lines An iterable sequence of lines, containing at least a private key attribute
         * @return An `Interface` with all of the attributes from `lines` set
         */
        @Throws(BadConfigException::class)
        fun parse(lines: Iterable<CharSequence?>): Interface {
            val builder = Builder()
            for (line in lines) {
                val attribute: Attribute =
                    Attribute.Companion.parse(line).orElseThrow<BadConfigException>(
                        Supplier<BadConfigException> { BadConfigException() })
                when (attribute.key.lowercase()) {
                    "address" -> builder.parseAddresses(attribute.value)
                    "dns" -> builder.parseDnsServers(attribute.value)
                    "excludedapplications" -> builder.parseExcludedApplications(attribute.value)
                    "includedapplications" -> builder.parseIncludedApplications(attribute.value)
                    "listenport" -> builder.parseListenPort(attribute.value)
                    "mtu" -> builder.parseMtu(attribute.value)
                    "privatekey" -> builder.parsePrivateKey(attribute.value)
                    "jc" -> builder.parseJunkPacketCount(attribute.value)
                    "jmin" -> builder.parseJunkPacketMinSize(attribute.value)
                    "jmax" -> builder.parseJunkPacketMaxSize(attribute.value)
                    "s1" -> builder.parseInitPacketJunkSize(attribute.value)
                    "s2" -> builder.parseResponsePacketJunkSize(attribute.value)
                    "h1" -> builder.parseInitPacketMagicHeader(attribute.value)
                    "h2" -> builder.parseResponsePacketMagicHeader(attribute.value)
                    "h3" -> builder.parseUnderloadPacketMagicHeader(attribute.value)
                    "h4" -> builder.parseTransportPacketMagicHeader(attribute.value)
                    else -> throw BadConfigException()
                }
            }
            return builder.build()
        }
    }
}
