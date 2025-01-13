/*
 * Copyright © 2017-2023 WireGuard LLC. All Rights Reserved.
 * SPDX-License-Identifier: Apache-2.0
 */
package com.dobby.awg.config

import java.io.BufferedReader
import java.io.IOException
import java.io.InputStream
import java.io.InputStreamReader
import java.util.Collections
import java.util.Objects

/**
 * Represents the contents of a awg-quick configuration file, made up of one or more "Interface"
 * sections (combined together), and zero or more "Peer" sections (treated individually).
 *
 *
 * Instances of this class are immutable.
 */
class Config private constructor(builder: Builder) {
    /**
     * Returns the interface section of the configuration.
     *
     * @return the interface configuration
     */
    val intface: Interface = builder.interfaze ?: throw RuntimeException("An [Interface] section is required")

    /**
     * Returns a list of the configuration's peer sections.
     *
     * @return a list of [Peer]s
     */
    // Defensively copy to ensure immutability even if the Builder is reused.
    val peers: List<Peer> = Collections.unmodifiableList(ArrayList(builder.peers))

    override fun equals(obj: Any?): Boolean {
        if (obj !is Config) return false
        val other = obj
        return intface == other.intface && peers == other.peers
    }

    override fun hashCode(): Int {
        return 31 * intface.hashCode() + peers.hashCode()
    }

    /**
     * Converts the `Config` into a string suitable for debugging purposes. The `Config`
     * is identified by its interface's public key and the number of peers it has.
     *
     * @return a concise single-line identifier for the `Config`
     */
    override fun toString(): String {
        return "(Config " + intface + " (" + peers.size + " peers))"
    }

    /**
     * Converts the `Config` into a string suitable for use as a `awg-quick`
     * configuration file.
     *
     * @return the `Config` represented as one [Interface] and zero or more [Peer] sections
     */
    fun toAwgQuickString(): String {
        val sb = StringBuilder()
        sb.append("[Interface]\n").append(intface.toAwgQuickString())
        for (peer in peers) sb.append("\n[Peer]\n").append(peer.toAwgQuickString())
        return sb.toString()
    }

    /**
     * Serializes the `Config` for use with the AmneziaWG cross-platform userspace API.
     *
     * @return the `Config` represented as a series of "key=value" lines
     */
    fun toAwgUserspaceString(): String {
        val sb = StringBuilder()
        sb.append(intface.toAwgUserspaceString())
        sb.append("replace_peers=true\n")
        for (peer in peers) sb.append(peer.toAwgUserspaceString())
        return sb.toString()
    }

    class Builder {
        // Defaults to an empty set.
        val peers: ArrayList<Peer> = ArrayList()

        // No default; must be provided before building.
        var interfaze: Interface? = null

        fun addPeer(peer: Peer): Builder {
            peers.add(peer)
            return this
        }

        fun addPeers(peers: Collection<Peer>?): Builder {
            this.peers.addAll(peers!!)
            return this
        }

        fun build(): Config {
            requireNotNull(interfaze) { "An [Interface] section is required" }
            return Config(this)
        }

        @Throws(BadConfigException::class)
        fun parseInterface(lines: Iterable<CharSequence?>): Builder {
            return setInterface(Interface.Companion.parse(lines))
        }

        @Throws(BadConfigException::class)
        fun parsePeer(lines: Iterable<CharSequence?>): Builder {
            return addPeer(Peer.Companion.parse(lines))
        }

        fun setInterface(interfaze: Interface?): Builder {
            this.interfaze = interfaze
            return this
        }
    }

    companion object {
        /**
         * Parses an series of "Interface" and "Peer" sections into a `Config`. Throws
         * [BadConfigException] if the input is not well-formed or contains data that cannot
         * be parsed.
         *
         * @param stream a stream of UTF-8 text that is interpreted as an AmneziaWG configuration
         * @return a `Config` instance representing the supplied configuration
         */
        @Throws(IOException::class, BadConfigException::class)
        fun parse(stream: InputStream?): Config {
            return parse(BufferedReader(InputStreamReader(stream)))
        }

        /**
         * Parses an series of "Interface" and "Peer" sections into a `Config`. Throws
         * [BadConfigException] if the input is not well-formed or contains data that cannot
         * be parsed.
         *
         * @param reader a BufferedReader of UTF-8 text that is interpreted as an AmneziaWG configuration
         * @return a `Config` instance representing the supplied configuration
         */
        @Throws(IOException::class, BadConfigException::class)
        fun parse(reader: BufferedReader): Config {
            val builder = Builder()
            val interfaceLines: MutableCollection<String?> = ArrayList()
            val peerLines: MutableCollection<String?> = ArrayList()
            var inInterfaceSection = false
            var inPeerSection = false
            var seenInterfaceSection = false
            var line: String?
            while ((reader.readLine().also { line = it }) != null) {
                val commentIndex = line!!.indexOf('#')
                if (commentIndex != -1) line = line!!.substring(0, commentIndex)
                line = line!!.trim { it <= ' ' }
                if (line!!.isEmpty()) continue
                if (line!!.startsWith("[")) {
                    // Consume all [Peer] lines read so far.
                    if (inPeerSection) {
                        builder.parsePeer(peerLines)
                        peerLines.clear()
                    }
                    if ("[Interface]".equals(line, ignoreCase = true)) {
                        inInterfaceSection = true
                        inPeerSection = false
                        seenInterfaceSection = true
                    } else if ("[Peer]".equals(line, ignoreCase = true)) {
                        inInterfaceSection = false
                        inPeerSection = true
                    } else {
                        throw BadConfigException()
                    }
                } else if (inInterfaceSection) {
                    interfaceLines.add(line)
                } else if (inPeerSection) {
                    peerLines.add(line)
                } else {
                    throw BadConfigException()
                }
            }
            if (inPeerSection) builder.parsePeer(peerLines)
            if (!seenInterfaceSection) throw BadConfigException()
            // Combine all [Interface] sections in the file.
            builder.parseInterface(interfaceLines)
            return builder.build()
        }
    }
}
