# frozen_string_literal: true

# Runs the calls Steep type-checks in test/typecheck/usage.rb.

require_relative "test_helper"
require_relative "typecheck/usage"

class TypecheckTest < Minitest::Test
  def test_usage_builds
    urls = Usage.urls
    assert_equal 16, urls.length
    assert(urls.all? { |u| u.is_a?(String) && !u.empty? })
    assert_equal "https://img.example.com/szYyuFhdpHbgPLsbLMU6HWJoN4Ok_gLUOYb76PJ9kLY/rs:fill:300:400/q:80/g:sm/czM6Ly9idWNrZXQvY2F0LmpwZw.webp",
                 urls.first
    helpers = Usage.helpers
    assert_equal "bytes=0-1023", helpers[6]
    assert_equal "x", helpers[8]
  end
end
