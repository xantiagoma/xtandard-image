# Module xtandard-image-kmp

The Kotlin Multiplatform client of [xtandard-image](https://github.com/xantiagoma/xtandard-image): typed URL
builders for the imgproxy dialect (processing, `/info`, `/video`, HLS and DASH) and the query dialect, imgproxy and
`xi_sig` signing, `enc/` source and PDF password encryption, and a Ktor HTTP client with suspend functions. It runs
on the JVM (servers, desktop, Android) and on Apple platforms (iOS, macOS).

Keys shipped inside an app can be extracted from it. Prefer backend-signed URLs, or the server's `restricted`
unsigned policy with presets and allowed sizes; see the README.

# Package io.github.xantiagoma.xtandard.image.kmp

URL builders ([ImgproxyUrlBuilder], [QueryUrlBuilder]), option lists ([ProcessingOptions], [InfoOptions],
[VideoOptions]), keys ([SigningKey], [EncryptionKey]), helpers ([Signing], [Encryption], [Sources]) and the HTTP
client ([XtandardClient]).
