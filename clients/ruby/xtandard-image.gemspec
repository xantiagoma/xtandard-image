# frozen_string_literal: true

require_relative "lib/xtandard_image/version"

Gem::Specification.new do |spec|
  spec.name = "xtandard-image"
  # Lockstep with the server's release tags (ADR 0013); 0.0.0 until the
  # first release. The release job sets it from the tag.
  spec.version = XtandardImage::VERSION
  spec.authors = ["Santiago Montoya"]
  spec.summary = "URL builder (imgproxy, /info, /video, HLS/DASH, query dialect), signing, " \
                 "source encryption and HTTP client for xtandard-image."
  spec.description = "The official Ruby client of xtandard-image, a self-hosted image and video " \
                     "processing server compatible with imgproxy URLs. Builds and signs processing, " \
                     "/info, /video, HLS/DASH and query-dialect URLs, encrypts sources and PDF " \
                     "passwords, and fetches media over Net::HTTP. No runtime dependencies."
  spec.homepage = "https://github.com/xantiagoma/xtandard-image/tree/main/clients/ruby#readme"
  spec.license = "MIT"
  spec.required_ruby_version = ">= 3.2"

  spec.metadata = {
    "homepage_uri" => spec.homepage,
    "source_code_uri" => "https://github.com/xantiagoma/xtandard-image/tree/main/clients/ruby",
    "bug_tracker_uri" => "https://github.com/xantiagoma/xtandard-image/issues",
    "rubygems_mfa_required" => "true",
  }

  spec.files = Dir["lib/**/*.rb", "sig/**/*.rbs", "README.md", "LICENSE"]
  spec.require_paths = ["lib"]
  # No runtime dependencies: openssl, json and net/http are default gems.
end
