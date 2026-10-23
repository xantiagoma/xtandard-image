// swift-tools-version:6.2
//
// The official Swift client of xtandard-image (clients/swift, ADR 0013).
//
// This manifest sits at the repository root only so SwiftPM can consume the
// repository directly (no mirror repository): every target lives under
// clients/swift/ and versions are the repository's own vX.Y.Z tags, in
// lockstep with the server. Develop and test the client from clients/swift
// (`task ci` there); see clients/swift/README.md.
//
//   .package(url: "https://github.com/xantiagoma/xtandard-image", from: "0.1.0")
//   .product(name: "XtandardImage", package: "xtandard-image")
import PackageDescription

/// Platforms without CryptoKit and CommonCrypto use swift-crypto.
let nonApple: [Platform] = [.linux, .android, .windows, .openbsd, .wasi]

let package = Package(
  name: "xtandard-image",
  platforms: [.iOS(.v15), .macOS(.v12), .tvOS(.v15), .watchOS(.v8), .visionOS(.v1)],
  products: [
    .library(name: "XtandardImage", targets: ["XtandardImage"])
  ],
  dependencies: [
    // HMAC-SHA256 and AES-CBC off Apple platforms (Apache-2.0; AES._CBC since 3.1.0). Apple
    // platforms use CryptoKit and CommonCrypto from the OS and never build it.
    .package(url: "https://github.com/apple/swift-crypto.git", "3.1.0"..<"6.0.0")
  ],
  targets: [
    .target(
      name: "XtandardImage",
      dependencies: [
        .product(name: "Crypto", package: "swift-crypto", condition: .when(platforms: nonApple)),
        .product(
          name: "_CryptoExtras", package: "swift-crypto", condition: .when(platforms: nonApple)),
      ],
      path: "clients/swift/Sources/XtandardImage"
    ),
    .testTarget(
      name: "XtandardImageTests",
      dependencies: ["XtandardImage"],
      path: "clients/swift/Tests/XtandardImageTests"
    ),
  ]
)
