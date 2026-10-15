//! Unit tests of the builders beyond the shared fixtures: the Rust-specific forms.

use xtandard_image::{
    Arg, ByteRange, ErrorKind, ImageFormat, ImaginaryEndpoint, ImgproxyUrlBuilder, IntoArgs, OptionNames, Options,
    Params, QueryUrlBuilder, QueryUrlParams, SegmentFormat, SourceEncoding, StreamParams, StreamProtocol, UrlParams,
    VideoContainer, VideoUrlParams, imgproxy, info, processing, query_urls, range_header, video,
};

const SRC: &str = "https://example.com/a.jpg";

#[test]
fn bare_values_tuples_and_none() {
    let opts = Options::new()
        .set("rs", (processing::ResizeType::Fill, 300, 400))
        .set("q", 80)
        .set("g", processing::GravityType::Sm)
        .set_opt("bl", None::<f64>);
    assert_eq!(
        imgproxy().url(SRC, &opts, UrlParams::new()).unwrap(),
        "/insecure/rs:fill:300:400/q:80/g:sm/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw"
    );
}

#[test]
fn setting_a_name_twice_replaces_it_in_place() {
    let opts = Options::new().set("w", 1).set("h", 2).set("w", 3);
    assert_eq!(opts.len(), 2);
    assert_eq!(opts.iter().next(), Some(("w", &[Arg::Int(3)][..])));
}

#[test]
fn arguments_of_every_shape() {
    assert_eq!(vec![1, 2].into_args(), vec![Arg::Int(1), Arg::Int(2)]);
    assert_eq!([0.5, 1.5].into_args(), vec![Arg::Float(0.5), Arg::Float(1.5)]);
    assert_eq!((&["a", "b"][..]).into_args(), vec![Arg::Str("a".into()), Arg::Str("b".into())]);
    assert_eq!(Some(2_u8).into_args(), vec![Arg::Int(2)]);
    assert_eq!(None::<i32>.into_args(), vec![Arg::Null]);
    assert_eq!(().into_args(), Vec::<Arg>::new());
    assert_eq!(
        ("fp", 0.5_f32, u64::MAX).into_args(),
        vec![Arg::Str("fp".into()), Arg::Float(0.5), Arg::Int(i128::from(u64::MAX))]
    );
}

#[test]
fn chained_pipelines_from_arrays_and_vecs() {
    let b = imgproxy().source_encoding(SourceEncoding::Plain);
    let want = "/insecure/rs:fit:100:100/-/bl:2/plain/https://example.com/a.jpg";
    let pls = [Options::new().set("rs", ("fit", 100, 100)), Options::new().set("bl", 2)];
    assert_eq!(b.url(SRC, pls.clone(), UrlParams::new()).unwrap(), want);
    assert_eq!(b.url(SRC, pls.to_vec(), UrlParams::new()).unwrap(), want);
    assert_eq!(b.url(SRC, &pls[..], UrlParams::new()).unwrap(), want);
    assert_eq!(b.url(SRC, (), UrlParams::new()).unwrap(), "/insecure/plain/https://example.com/a.jpg");
}

#[test]
fn fluent_methods_use_the_name_called() {
    let b = imgproxy().option_names(OptionNames::Given).source_encoding(SourceEncoding::Plain);
    assert_eq!(
        b.image(SRC).resize(("fit", 1, 2)).q(90).min_width(10).fn_("x").build().unwrap(),
        "/insecure/resize:fit:1:2/q:90/min-width:10/fn:x/plain/https://example.com/a.jpg"
    );
    assert_eq!(b.info(SRC).do_(true).build().unwrap(), "/info/insecure/do:1/plain/https://example.com/a.jpg");
}

#[test]
fn option_names_by_enum() {
    let opt: processing::OptionName = "rs".parse().unwrap();
    assert_eq!(opt, processing::OptionName::Resize);
    assert_eq!((opt.name(), opt.short()), ("resize", "rs"));
    assert!(opt.aliases().contains(&"rs"));
    assert_eq!("nope".parse::<processing::OptionName>().unwrap_err().code(), "unknown_option");
    assert_eq!(
        imgproxy().image(SRC).option(processing::OptionName::Quality, 70).build().unwrap(),
        imgproxy().image(SRC).q(70).build().unwrap()
    );
    assert!(info::OptionName::ALL.len() >= 30);
    assert!(video::OptionName::ALL.len() >= 45);
    assert_eq!(video::OptionName::Start.short(), "vs");
}

#[test]
fn argument_enums() {
    assert_eq!(processing::ResizeType::FillDown.as_str(), "fill-down");
    assert_eq!("fill-down".parse::<processing::ResizeType>().unwrap(), processing::ResizeType::FillDown);
    assert_eq!("nope".parse::<processing::ResizeType>().unwrap_err().code(), "invalid_argument");
    assert_eq!(processing::ResizeType::Fit.to_string(), "fit");
    assert_eq!(video::StreamProtocol::Hls.as_str(), "hls");
}

#[test]
fn constants() {
    assert_eq!(ImageFormat::Webp.as_str(), "webp");
    assert_eq!(VideoContainer::M3u8.as_str(), "m3u8");
    assert_eq!(StreamProtocol::Dash.manifest_container(), "mpd");
    assert_eq!(StreamProtocol::Hls.content_type(), "application/vnd.apple.mpegurl");
    assert_eq!("cmaf".parse::<SegmentFormat>().unwrap(), SegmentFormat::Fmp4);
    assert_eq!(SegmentFormat::DEFAULT, SegmentFormat::Fmp4);
    assert_eq!(SegmentFormat::Ts.protocols(), &[StreamProtocol::Hls]);
    assert_eq!("bogus".parse::<StreamProtocol>().unwrap_err().code(), "invalid_request");
    assert_eq!(ErrorKind::from_kind("not_found").map(ErrorKind::status), Some(404));
    assert_eq!(ErrorKind::Overloaded.message(), "too many requests");
}

#[test]
fn extensions_take_format_enums() {
    let b = imgproxy();
    assert_eq!(
        b.url(SRC, (), UrlParams::new().extension(ImageFormat::Avif)).unwrap(),
        "/insecure/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw.avif"
    );
    assert_eq!(
        b.video_url("s3://m/a.mov", Options::new().set("vd", 5), VideoUrlParams::new().extension(VideoContainer::Webm))
            .unwrap(),
        "/video/insecure/vd:5/czM6Ly9tL2EubW92.webm"
    );
}

#[test]
fn video_and_stream() {
    let b = ImgproxyUrlBuilder::new().source_encoding(SourceEncoding::Plain);
    let opts = Options::new().set("rs", ("fill", 640, 360)).set("vs", 1.5).set("vd", 5);
    assert_eq!(
        b.video_url("s3://m/a.mov", &opts, VideoUrlParams::new().extension("webm")).unwrap(),
        "/video/insecure/rs:fill:640:360/vs:1.5/vd:5/plain/s3://m/a.mov@webm"
    );
    assert_eq!(
        b.video("s3://m/a.mov").start(2).mute(true).extension("mp4").build().unwrap(),
        "/video/insecure/vs:2/mu:1/plain/s3://m/a.mov@mp4"
    );
    assert_eq!(
        b.stream_url("s3://m/a.mov", &StreamParams::new(StreamProtocol::Dash).renditions([360])).unwrap(),
        "/video/insecure/st:dash:360/plain/s3://m/a.mov@mpd"
    );
}

#[test]
fn query_dialect() {
    let q = QueryUrlBuilder::new();
    assert_eq!(
        q.url(SRC, (), QueryUrlParams::new().param("w", 300).param("enlarge", true)).unwrap(),
        "/q/?url=https://example.com/a.jpg&w=300&enlarge=true"
    );
    assert_eq!(q.image(SRC).param("w", 300).bl(2).build().unwrap(), "/q/?url=https://example.com/a.jpg&w=300&o=bl:2");
    assert_eq!(
        query_urls().imaginary_url(ImaginaryEndpoint::Resize, SRC, Params::new().add("width", 10)).unwrap(),
        "/q/resize?url=https://example.com/a.jpg&width=10"
    );
    assert_eq!(
        q.video("s3://m/a.mov").crop((100, 100)).flip((true, false)).extension("webm").build().unwrap(),
        "/q/video?url=s3://m/a.mov&c=100,100&fl=1,0&format=webm"
    );
    let st = StreamParams::new(StreamProtocol::Hls).source_encoding(SourceEncoding::Plain);
    assert_eq!(q.stream_url("s3://m/a.mov", &st).unwrap_err().code(), "invalid_request");
}

#[test]
fn errors_carry_codes() {
    let b = imgproxy();
    assert_eq!(b.image(SRC).option("nope", 1).build().unwrap_err().code(), "unknown_option");
    assert_eq!(b.image(SRC).w(()).build().unwrap_err().code(), "invalid_argument");
    assert_eq!(b.image(SRC).w(f64::NAN).build().unwrap_err().code(), "invalid_argument");
    assert_eq!(b.image(SRC).fn_("a\u{feff}b").build().unwrap_err().code(), "invalid_argument");
    assert_eq!(b.image("").build().unwrap_err().code(), "invalid_source");
    assert_eq!(b.image(SRC).extension("we.bp").build().unwrap_err().code(), "invalid_extension");
    assert_eq!(b.clone().separator('-').image(SRC).build().unwrap_err().code(), "invalid_separator");
    assert_eq!(b.image(SRC).encoding(SourceEncoding::Encrypted).build().unwrap_err().code(), "missing_encryption");
    let e = b.image(SRC).option("nope", 1).build().unwrap_err();
    assert!(e.to_string().contains("nope"), "{e}");
    assert_eq!(e.message(), e.to_string());
}

#[test]
fn ranges() {
    assert_eq!(range_header(&(0..1024).into()).unwrap(), "bytes=0-1023");
    assert_eq!(range_header(&(5..=9).into()).unwrap(), "bytes=5-9");
    assert_eq!(range_header(&(100..).into()).unwrap(), "bytes=100-");
    assert_eq!(range_header(&(3..3).into()).unwrap_err().code(), "invalid_range");
    assert_eq!(range_header(&ByteRange::suffix(4)).unwrap(), "bytes=-4");
}

#[test]
fn chains_build_repeatedly() {
    let b = imgproxy();
    let chain = b.image(SRC).w(10);
    assert_eq!(chain.build().unwrap(), chain.clone().build().unwrap());
}
