package com.dobby.domain

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import android.content.SharedPreferences
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Stores only AES-GCM ciphertext in SharedPreferences. The non-exportable key
 * remains in AndroidKeyStore and plaintext legacy values are deleted only
 * after an encrypted write succeeds.
 */
internal class AndroidKeystoreSecretStore(
    private val preferences: SharedPreferences,
) {
    private val key: SecretKey by lazy(::loadOrCreateKey)

    fun migrate(keys: Collection<String>) {
        keys.forEach { name ->
            // A previous successful write can leave a duplicate legacy value
            // behind if the process died before its cleanup commit. It is
            // never a read fallback: remove it on every idempotent migration.
            if (preferences.contains(secureName(name))) {
                preferences.edit().remove(name).commit()
                return@forEach
            }
            if (!preferences.contains(name)) return@forEach
            val plaintext = preferences.getString(name, null) ?: return@forEach
            write(name, plaintext) // commits ciphertext and plaintext removal together
        }
    }

    fun read(name: String, default: String = ""): String {
        val encoded = preferences.getString(secureName(name), null) ?: return default
        return runCatching {
            val payload = Base64.decode(encoded, Base64.NO_WRAP)
            require(payload.size > IV_BYTES)
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(
                Cipher.DECRYPT_MODE,
                key,
                GCMParameterSpec(TAG_BITS, payload, 0, IV_BYTES),
            )
            cipher.doFinal(payload, IV_BYTES, payload.size - IV_BYTES).decodeToString()
        }.getOrDefault(default)
    }

    fun write(name: String, value: String): Boolean = runCatching {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key)
        val ciphertext = cipher.doFinal(value.encodeToByteArray())
        val payload = cipher.iv + ciphertext
        preferences.edit()
            .putString(secureName(name), Base64.encodeToString(payload, Base64.NO_WRAP))
            .remove(name)
            .commit()
    }.getOrDefault(false)

    private fun loadOrCreateKey(): SecretKey {
        val store = KeyStore.getInstance(KEYSTORE).apply { load(null) }
        (store.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE).run {
            init(
                KeyGenParameterSpec.Builder(
                    KEY_ALIAS,
                    KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
                )
                    .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                    .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                    .setRandomizedEncryptionRequired(true)
                    .build(),
            )
            generateKey()
        }
    }

    private fun secureName(name: String) = "secure.v1.$name"

    private companion object {
        const val KEYSTORE = "AndroidKeyStore"
        const val KEY_ALIAS = "dobbyvpn.config.secrets.v1"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val IV_BYTES = 12
        const val TAG_BITS = 128
    }
}
