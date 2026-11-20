#!/usr/bin/env ruby
# frozen_string_literal: true

# Render and statically verify both release-asset identities before writing.
# Usage: render.rb TEMPLATE OUT VERSION ARM_SHA AMD_SHA METADATA_PARSER
require 'json'
require 'open3'

abort 'usage: render.rb TEMPLATE OUT VERSION ARM_SHA AMD_SHA METADATA_PARSER' unless ARGV.length == 6
template, out, version, arm_sha, amd_sha, parser = ARGV
stable = /(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)/
abort 'Invalid version' unless version.match?(/\A#{stable}\z/)
abort 'Invalid archive SHA256' unless [arm_sha, amd_sha].all? { |sha| sha.match?(/\A[a-fA-F0-9]{64}\z/) }
base = "https://github.com/puppetdevz/ianchor/releases/download/v#{version}/ianchor_#{version}_darwin"
values = { 'VERSION' => version, 'ARM_URL' => "#{base}_arm64.tar.gz", 'AMD_URL' => "#{base}_amd64.tar.gz",
           'ARM_SHA256' => arm_sha, 'AMD_SHA256' => amd_sha }
source = File.read(template)
values.each do |key, value|
  marker = "__IANCHOR_#{key}__"
  abort "Expected one #{marker}" unless source.scan(marker).length == 1

  source = source.sub(marker, value)
end
abort 'Unresolved template field' if source.include?('__IANCHOR_')

declarations, status = Open3.capture2('ruby', parser, stdin_data: source)
abort 'Rendered Formula is not a static Ianchor < Formula class' unless status.success?
data = JSON.parse(declarations)
lines = source.split(/\r?\n/)
verify = lambda do |group, key, expected|
  entries = group[key] || []
  abort "Expected exactly one rendered #{key}" unless entries.length == 1
  line, column = entries[0]
  text = lines[line - 1].to_s
  value = text.match(/\A[\t ]*#{key}[\t ]+"([^"]+)"[\t ]*(?:#[^\r\n]*)?\z/)
  abort "Incorrect rendered #{key}" unless value && value[1] == expected && text[0...column].to_s.strip.empty?
end
verify.call(data, 'version', version)
abort 'Unexpected unconditional metadata or dependency' unless %w[url sha256 depends_on].all? { |key| (data[key] || []).empty? }
branches = data.fetch('branches')
abort 'Expected exactly one arm and one intel block' unless branches.keys.sort == %w[on_arm on_intel]
verify.call(branches['on_arm'], 'url', values['ARM_URL'])
verify.call(branches['on_arm'], 'sha256', arm_sha)
verify.call(branches['on_intel'], 'url', values['AMD_URL'])
verify.call(branches['on_intel'], 'sha256', amd_sha)
File.write(out, source)
