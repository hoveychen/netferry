use aes_gcm::aead::{Aead, KeyInit};
use aes_gcm::{Aes256Gcm, Nonce};
use base64::engine::general_purpose::STANDARD as B64;
use base64::Engine;
use rand::RngCore;

fn export_key() -> Result<[u8; 32], String> {
    let hex = option_env!("NETFERRY_EXPORT_KEY")
        .ok_or("Profile export is not available in this build")?;
    let hex = hex.trim();
    if hex.len() != 64 {
        return Err("NETFERRY_EXPORT_KEY must be 64 hex characters (32 bytes)".into());
    }
    let mut key = [0u8; 32];
    for (i, chunk) in hex.as_bytes().chunks(2).enumerate() {
        key[i] = u8::from_str_radix(std::str::from_utf8(chunk).unwrap(), 16)
            .map_err(|_| "NETFERRY_EXPORT_KEY contains invalid hex")?;
    }
    Ok(key)
}

/// Encrypt plaintext bytes with AES-256-GCM.
/// Returns base64-encoded string: nonce (12 bytes) || ciphertext+tag.
pub fn encrypt(plaintext: &[u8]) -> Result<String, String> {
    let key = export_key()?;
    let cipher = Aes256Gcm::new_from_slice(&key).map_err(|e| e.to_string())?;
    let mut nonce_bytes = [0u8; 12];
    rand::thread_rng().fill_bytes(&mut nonce_bytes);
    let nonce = Nonce::from_slice(&nonce_bytes);
    let ciphertext = cipher
        .encrypt(nonce, plaintext)
        .map_err(|e| format!("Encryption failed: {e}"))?;
    let mut combined = nonce_bytes.to_vec();
    combined.extend_from_slice(&ciphertext);
    Ok(B64.encode(&combined))
}

/// Decrypt a base64-encoded string produced by `encrypt`.
pub fn decrypt(encoded: &str) -> Result<Vec<u8>, String> {
    let key = export_key()?;
    let combined = B64
        .decode(encoded.trim())
        .map_err(|e| format!("Invalid base64: {e}"))?;
    if combined.len() < 13 {
        return Err("Encrypted data too short".into());
    }
    let (nonce_bytes, ciphertext) = combined.split_at(12);
    let cipher = Aes256Gcm::new_from_slice(&key).map_err(|e| e.to_string())?;
    let nonce = Nonce::from_slice(nonce_bytes);
    cipher
        .decrypt(nonce, ciphertext)
        .map_err(|_| "Decryption failed — wrong key or corrupted data".into())
}
