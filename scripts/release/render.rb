#!/usr/bin/env ruby
# frozen_string_literal: true

# Renders the Formula template and verifies the result contains exactly the
# intended static declarations.
# Usage: render.rb TEMPLATE OUT VERSION URL SHA256 METADATA_PARSER
require 'json'
require 'open3'

template, out, version, url, sha, parser = ARGV
abort 'usage: render.rb TEMPLATE OUT VERSION URL SHA256 METADATA_PARSER' unless parser
abort 'Invalid archive SHA256' unless sha.match?(/\A[a-fA-F0-9]{64}\z/)

source = File.read(template)
{ 'VERSION' => version, 'URL' => url, 'SHA256' => sha }.each do |key, value|
  marker = "__IANCHOR_#{key}__"
  abort "Expected one #{marker}" unless source.scan(marker).length == 1

  source = source.sub(marker, value)
end
abort 'Unresolved template field' if source.include?('__IANCHOR_')

declarations, status = Open3.capture2('ruby', parser, stdin_data: source)
abort 'Rendered Formula is not a static Ianchor < Formula class' unless status.success?
data = JSON.parse(declarations)
lines = source.split(/\r?\n/)
{ 'version' => version, 'url' => url, 'sha256' => sha, 'depends_on' => 'go' }.each do |key, expected|
  entries = data[key] || []
  abort "Expected exactly one real rendered #{key} declaration" unless entries.length == 1

  line, column = entries[0]
  text = lines[line - 1].to_s
  suffix = key == 'depends_on' ? '(?:\\s*=>\\s*:build)?' : ''
  value = text.match(/\A[\t ]*#{key}[\t ]+"([^"]+)"#{suffix}[\t ]*(?:#[^\r\n]*)?\z/)
  abort "Unsupported rendered #{key} declaration" unless value && text[0...column].to_s.strip.empty?
  abort "Incorrect rendered #{key}" unless value[1] == expected
end
File.write(out, source)
