# Keys in apps

Signing and encryption work in iOS, macOS, tvOS, watchOS and visionOS apps, but a key shipped in an app can be extracted.

## Overview

A signing key signs **every** URL, and an encryption key decrypts **every** `enc/` source and
PDF password. Anything compiled into an app binary, stored in its bundle, or fetched to the
device can be read by someone who has the app: from the binary, from memory on a jailbroken
device, or from the network. Obfuscation slows that down; it does not prevent it.

``SigningKey`` and ``EncryptionKey`` are fully supported on every platform, so you can use them
in a server-side Swift app (Vapor, Hummingbird) or in tools. In an app shipped to users, prefer
one of these instead:

- **Unsigned URLs in `restricted` mode.** Run the server with its unsigned policy set to
  `restricted` and a list of presets or allowed widths. The app builds unsigned URLs
  (``ImgproxyURLBuilder`` without `signing:`), and the server only answers the sizes and
  presets you allowed, so nobody can make it resize to arbitrary sizes.
- **Backend-signed URLs.** Your backend, which holds the key, returns signed URLs (or signs the
  URLs the app asks for, after checking them). The app only fetches them, with
  ``XtandardClient/media(_:_:)`` or any image loader.

Never ship an encryption key in an app: `enc/` exists to hide source URLs, and a key in the app
reveals all of them.
