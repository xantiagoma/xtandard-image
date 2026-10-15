# frozen_string_literal: true

$LOAD_PATH.unshift(File.expand_path("../lib", __dir__))

require "json"
require "minitest/autorun"
require "xtandard_image"

# Helpers shared by the tests.
module TestHelper
  # clients/ruby/test -> the repository root.
  ROOT = File.expand_path("../../..", __dir__)

  module_function

  # Parses a JSON file of the repository by its path from the root.
  def load_json(rel)
    JSON.parse(File.read(File.join(ROOT, rel), encoding: "UTF-8"))
  end

  # A test method name from a case name.
  def test_name(*parts)
    "test_#{parts.join("_").gsub(/[^A-Za-z0-9_]+/, "_")}"
  end
end
