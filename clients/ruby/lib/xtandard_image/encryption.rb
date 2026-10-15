# frozen_string_literal: true

module XtandardImage
  # An encryption key: the first of XI_ENCRYPTION_KEYS (imgproxy
  # IMGPROXY_SOURCE_URL_ENCRYPTION_KEY).
  #
  # +key+ is an AES key of 16, 24 or 32 bytes, hex or a binary String. Leave
  # +iv+ nil for a fresh random IV per token, as the server's own encoder
  # does. A fixed 16-byte IV makes URLs deterministic and reveals when two
  # URLs share a source; use it only for tests or cache keys. +inspect+
  # hides both.
  EncryptionKey = Data.define(:key, :iv)

  # Checks the key and IV sizes; hides the secrets from inspect.
  class EncryptionKey
    def initialize(key:, iv: nil)
      k = Text.key_bytes(key, "encryption key")
      unless ENCRYPTION_KEY_SIZES.include?(k.bytesize)
        raise URLBuildError.new(:invalid_key, "encryption key is #{k.bytesize} bytes, want 16, 24 or 32")
      end
      raise URLBuildError.new(:invalid_key, "iv is not #{IV_SIZE} bytes") if iv && Text.key_bytes(iv, "iv").bytesize != IV_SIZE

      super
    end

    def inspect = "#<data #{self.class.name} bits=#{Text.key_bytes(key, "encryption key").bytesize * 8}>"
    alias to_s inspect
  end

  # Source URL and PDF password encryption, byte-compatible with the server
  # (internal/security/crypt):
  #
  #   token = base64url_unpadded(IV[16] ‖ AES-CBC(key, IV, PKCS#7(utf8(plaintext))))
  #
  # Encrypt on a server: the key decrypts every token.
  module Encryption
    module_function

    # Encrypts a source URL into an enc/ token.
    #
    # Raises URLBuildError (:invalid_source) when the source is empty or has
    # control characters.
    def encrypt_source(source, key)
      seal(Text.check_text(source, :invalid_source, "source"), key)
    end

    # Encrypts the password of a password-protected PDF into a pdf_password
    # (pdp) token: the enc/ format over the password, with the same key. The
    # server accepts the option on signed URLs only.
    #
    # Raises URLBuildError (:invalid_password) when the password is empty,
    # has control characters or is longer than 1024 UTF-8 bytes.
    def encrypt_pdf_password(password, key)
      Text.check_text(password, :invalid_password, "password")
      if Text.utf8(password).bytesize > MAX_PDF_PASSWORD_BYTES
        raise URLBuildError.new(:invalid_password, "password is longer than #{MAX_PDF_PASSWORD_BYTES} bytes")
      end

      seal(password, key)
    end

    def seal(plaintext, key)
      k = Text.key_bytes(key.key, "encryption key")
      cipher = OpenSSL::Cipher.new("aes-#{k.bytesize * 8}-cbc")
      cipher.encrypt
      cipher.key = k
      fixed = key.iv
      iv = fixed.nil? ? cipher.random_iv : Text.key_bytes(fixed, "iv")
      cipher.iv = iv
      Text.b64url(iv + cipher.update(Text.utf8(plaintext).b) + cipher.final)
    end
  end
end
