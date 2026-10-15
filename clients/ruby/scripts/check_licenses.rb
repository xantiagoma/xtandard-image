# frozen_string_literal: true

# Checks the licences of the gem's runtime dependency closure (ADR 0006
# allowlist, ADR 0013). Run in the bundle: `bundle exec ruby scripts/check_licenses.rb`.
#
# The gemspec has no runtime dependencies, so the closure is the standard
# library gems lib/ requires (json, net/http, openssl, uri) and theirs. A
# require in lib/ that is not listed in STDLIB fails the check, so a new one
# is reviewed here first.
#
# Allowed: MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0, ISC. A gem's
# licences are alternatives (RubyGems convention): one allowed licence is
# enough. The exceptions are listed with their reason; anything else fails.

ROOT = File.expand_path("..", __dir__)
ALLOWED = /\A(MIT|MIT-0|BSD-2-Clause|BSD-3-Clause|Apache-2\.0|ISC)\z/
# Gems whose licence is outside the allowlist, with the reason they are accepted.
EXCEPTIONS = {
  # The Ruby licence (COPYING of Ruby and of the json gem) lets anyone
  # redistribute and modify under either the 2-clause BSD licence or the
  # Ruby terms; json declares only "Ruby".
  "json" => /\ARuby\z/,
}.freeze
# Feature name required in lib/ => gem name.
STDLIB = { "json" => "json", "net/http" => "net-http", "openssl" => "openssl", "uri" => "uri" }.freeze
# Gems net/http loads without declaring them.
INDIRECT = %w[net-protocol timeout zlib].freeze

def required_features
  Dir[File.join(ROOT, "lib/**/*.rb")].flat_map do |f|
    File.read(f).scan(/^\s*require\s+["']([^"']+)["']/).flatten
  end.uniq.sort
end

def spec_for(name)
  Gem.loaded_specs[name] || Gem::Specification.find_all_by_name(name).max_by(&:version)
end

def closure(roots)
  seen = {}
  todo = roots.dup
  until todo.empty?
    name = todo.pop
    next if seen.key?(name)

    spec = spec_for(name)
    abort "gem #{name} is not installed" if spec.nil?
    seen[name] = spec
    todo.concat(spec.runtime_dependencies.map(&:name))
  end
  seen.values.sort_by(&:name)
end

unknown = required_features - STDLIB.keys
abort "lib/ requires #{unknown.join(", ")}: review their licences, then add them to STDLIB" if unknown.any?

gemspec = Gem::Specification.load(File.join(ROOT, "xtandard-image.gemspec"))
abort "cannot load xtandard-image.gemspec" if gemspec.nil?

deps = gemspec.runtime_dependencies
bad = 0
closure(deps.map(&:name) + STDLIB.values + INDIRECT).each do |spec|
  licences = spec.licenses
  status =
    if licences.any? { |l| ALLOWED.match?(l) }
      "ok"
    elsif EXCEPTIONS.key?(spec.name) && licences.any? { |l| EXCEPTIONS.fetch(spec.name).match?(l) }
      "exception"
    else
      bad += 1
      "NOT ALLOWED"
    end
  shown = licences.empty? ? "UNKNOWN" : licences.join(" OR ")
  puts "#{status.ljust(12)} #{spec.name} #{spec.version}: #{shown}"
end
puts "runtime dependencies of xtandard-image: #{deps.empty? ? "none" : deps.join(", ")}"
exit(bad.zero? ? 0 : 1)
