#!/usr/bin/env ruby
# frozen_string_literal: true

# Validates the Formula currently published in the tap and refuses downgrades.
# Usage: check-existing.rb FORMULA TARGET_VERSION METADATA_PARSER
require 'json'
require 'open3'

formula, target, parser, target_url, target_sha = ARGV
abort 'usage: check-existing.rb FORMULA TARGET_VERSION METADATA_PARSER' unless formula && target && parser
exit 0 unless File.exist?(formula)

source = File.read(formula)
declarations, status = Open3.capture2('ruby', parser, stdin_data: source)
abort 'Cannot analyse existing Formula' unless status.success?
data = JSON.parse(declarations)
lines = source.split(/\r?\n/)
fields = lambda do |key|
  (data[key] || []).map do |line, column|
    text = lines[line - 1].to_s
    value = text.match(/\A[\t ]*#{key}[\t ]+["']([^"']+)["'][\t ]*(?:#[^\r\n]*)?\z/)
    abort "Unsupported existing #{key} declaration" unless value && text[0...column].to_s.strip.empty?

    value[1]
  end
end

versions = fields.call('version')
urls = fields.call('url')
shas = fields.call('sha256')
abort 'Existing Formula must have exactly one source URL' unless urls.length == 1
abort 'Existing Formula must have exactly one checksum' unless shas.length == 1
abort 'Invalid existing checksum' unless shas[0].match?(/\A[a-fA-F0-9]{64}\z/)
abort 'Ambiguous existing version' if versions.length > 1
stable = /(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)/
tag_match = urls[0].match(%r{\Ahttps://github\.com/puppetdevz/ianchor/archive/(?:refs/tags/)?v(#{stable})\.tar\.gz\z})
commit_match = urls[0].match(%r{\Ahttps://github\.com/puppetdevz/ianchor/archive/[0-9a-f]{40}\.tar\.gz\z})
abort 'Cannot identify existing stable version' unless tag_match || commit_match
abort 'Commit archive requires explicit version' if commit_match && versions.length != 1
old = versions[0] || tag_match[1]
abort 'Invalid existing version' unless old.match?(/\A#{stable}\z/)
abort 'Conflicting existing version and URL' if tag_match && old != tag_match[1]

current = target.split('.').map(&:to_i)
published = old.split('.').map(&:to_i)
3.times do |index|
  break if current[index] > published[index]

  if current[index] < published[index]
    warn "Skipping tap downgrade from #{old} to #{target}"
    exit 3
  end
end
if old == target && target_url && (urls[0] != target_url || shas[0].downcase != target_sha.to_s.downcase)
  abort "Refusing to change source identity for existing version #{old}"
end
