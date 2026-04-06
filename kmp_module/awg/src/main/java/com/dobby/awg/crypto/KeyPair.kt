/*
 * Copyright © 2017-2023 WireGuard LLC. All Rights Reserved.
 * SPDX-License-Identifier: Apache-2.0
 */
package com.dobby.awg.crypto

/**
 * Represents a Curve25519 key pair as used by AmneziaWG.
 *
 *
 * Instances of this class are immutable.
 */
class KeyPair @JvmOverloads constructor(
    /**
     * Returns the private key from the key pair.
     *
     * @return the private key
     */
    val privateKey: Key = Key.Companion.generatePrivateKey()
) {
    /**
     * Returns the public key from the key pair.
     *
     * @return the public key
     */
    val publicKey: Key = Key.Companion.generatePublicKey(privateKey)

    /**
     * Creates a key pair using an existing private key.
     *
     * @param privateKey a private key, used to derive the public key
     */
}
