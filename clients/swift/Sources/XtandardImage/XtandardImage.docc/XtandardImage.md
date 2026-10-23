# ``XtandardImage``

The official Swift client of xtandard-image: URL builders, signing, encryption and an async HTTP client.

## Overview

xtandard-image is a self-hosted image and video processing server with imgproxy-compatible URLs.
This package builds its URLs byte for byte like every other official client (they all run the
same conformance fixture), signs and encrypts them, and talks to the server over `URLSession`.

```swift
import XtandardImage

let img = imgproxy(
    baseURL: "https://img.example.com",
    signing: try SigningKey(key: "943b…0881", salt: "520f…09c5"))

// Mapping form: option names or aliases, in URL order.
let url = try img.url(
    "https://example.com/images/cat.jpg",
    options: ["rs": [ProcessingOption.ResizeType.fit, 300, 300]],
    extension: "png")

// Fluent form: one method per option name and alias.
let same = try img.image("https://example.com/images/cat.jpg")
    .resize(ProcessingOption.ResizeType.fit, 300, 300)
    .extension("png")
    .build()
```

Builders and keys are `Sendable` values, so one builder can be shared across tasks. Every
builder error is a ``URLBuildError`` whose ``URLBuildError/code`` is the language-neutral
error code of `clients/spec/conformance.json`.

The package works on iOS 15+, macOS 12+, tvOS 15+, watchOS 8+, visionOS 1+ and Linux. On Apple
platforms HMAC-SHA256 comes from CryptoKit and AES-CBC from CommonCrypto; on Linux both come
from swift-crypto.

## Topics

### Essentials

- <doc:KeysInApps>
- ``ImgproxyURLBuilder``
- ``imgproxy(baseURL:signing:unsignedSignature:encryption:sourceEncoding:splitSource:separator:optionNames:)``
- ``Options``
- ``Args``
- ``Arg``
- ``URLBuildError``

### Fluent builders

- ``ImgproxyURLChain``
- ``ImgproxyInfoURLChain``
- ``ImgproxyVideoURLChain``
- ``OptionChain``
- ``ProcessingOptionMethods``
- ``InfoOptionMethods``
- ``VideoOptionMethods``

### Query dialect

- ``QueryURLBuilder``
- ``queryURLs(baseURL:mount:signing:encryption:separator:optionNames:)``
- ``QueryURLChain``
- ``QueryVideoURLChain``
- ``Params``

### Signing and encryption

- ``SigningKey``
- ``sign(_:key:)``
- ``signImgproxyPath(_:key:)``
- ``xiMessage(path:rawQuery:)``
- ``signXiQuery(path:rawQuery:key:)``
- ``signXiURL(_:key:mount:)``
- ``EncryptionKey``
- ``encryptSource(_:key:)``
- ``encryptPDFPassword(_:key:)``

### Sources and escaping

- ``xiSource(_:_:)``
- ``sourceURL(_:_:_:)``
- ``escapePlainSource(_:)``
- ``escapeQueryValue(_:)``

### HTTP client

- ``XtandardClient``
- ``MediaOptions``
- ``MediaResponse``
- ``ByteRange``
- ``rangeHeader(_:)``
- ``QueryMethodRequest``
- ``queryMethodRequest(_:)``
- ``HTTPMethod``
- ``StreamManifest``
- ``Readiness``
- ``ClientError``
- ``HTTPError``
- ``TransportError``

### Generated catalogues

- ``ProcessingOption``
- ``InfoOption``
- ``VideoOption``
- ``OptionNames``
- ``ImageFormat``
- ``VideoContainer``
- ``StreamProtocol``
- ``SegmentFormat``
- ``SourceScheme``
- ``SourceEncoding``
- ``ImaginaryEndpoint``
- ``ErrorKind``
- ``SpecConstants``

### Responses

- ``InfoResponse``
- ``ErrorResponse``
- ``JSONValue``
