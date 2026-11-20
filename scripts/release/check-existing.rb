#!/usr/bin/env ruby
# frozen_string_literal: true

# Validate published metadata without evaluating the Formula; refuse downgrades.
# Usage: check-existing.rb FORMULA TARGET_VERSION METADATA_PARSER [ARM_SHA AMD_SHA]
require 'json'
require 'open3'

abort 'usage: check-existing.rb FORMULA TARGET_VERSION METADATA_PARSER [ARM_SHA AMD_SHA]' unless [3, 5].include?(ARGV.length)
formula, target, parser, arm_sha, amd_sha = ARGV
stable = /(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)/
checksum = /\A[a-fA-F0-9]{64}\z/
abort 'Invalid target version' unless target.match?(/\A#{stable}\z/)
if arm_sha && (!arm_sha.match?(checksum) || !amd_sha.match?(checksum))
  abort 'Invalid target checksum'
end
exit 0 unless File.exist?(formula)

source = File.read(formula)
declarations, status = Open3.capture2('ruby', parser, stdin_data: source)
abort 'Cannot analyse existing Formula' unless status.success?
data = JSON.parse(declarations)
lines = source.split(/\r?\n/)
fields = lambda do |group, key|
  (group[key] || []).map do |line, column|
    text = lines[line - 1].to_s
    value = text.match(/\A[\t ]*#{key}[\t ]+(["'])([^"'\\]+)\1[\t ]*(?:#[^\r\n]*)?\z/)
    abort "Unsupported existing #{key} declaration" unless value && text[0...column].to_s.strip.empty?

    value[2]
  end
end
versions = fields.call(data, 'version')
abort 'Ambiguous existing version' if versions.length > 1
branches = data.fetch('branches')
if branches.empty?
  urls = fields.call(data, 'url')
  shas = fields.call(data, 'sha256')
  abort 'Existing Formula must have exactly one source URL and checksum' unless urls.length == 1 && shas.length == 1
  abort 'Invalid existing checksum' unless shas[0].match?(checksum)
  tag = urls[0].match(%r{\Ahttps://github\.com/puppetdevz/ianchor/archive/(?:refs/tags/)?v(#{stable})\.tar\.gz\z})
  commit = urls[0].match(%r{\Ahttps://github\.com/puppetdevz/ianchor/archive/[0-9a-f]{40}\.tar\.gz\z})
  abort 'Cannot identify existing stable version' unless tag || commit
  abort 'Commit archive requires explicit version' if commit && versions.length != 1
  old = versions[0] || tag[1]
  abort 'Invalid existing version' unless old.match?(/\A#{stable}\z/)
  abort 'Conflicting existing version and URL' if tag && old != tag[1]
else
  abort 'Expected exactly one arm and one intel block' unless branches.keys.sort == %w[on_arm on_intel]
  abort 'Unexpected unconditional binary metadata' unless fields.call(data, 'url').empty? && fields.call(data, 'sha256').empty?
  abort 'Binary Formula must not have dependencies' unless (data['depends_on'] || []).empty?
  abort 'Binary Formula requires one explicit version' unless versions.length == 1 && versions[0].match?(/\A#{stable}\z/)
  old = versions[0]
  %w[arm64 amd64].zip(%w[on_arm on_intel]).each do |arch, name|
    urls = fields.call(branches[name], 'url')
    shas = fields.call(branches[name], 'sha256')
    expected = "https://github.com/puppetdevz/ianchor/releases/download/v#{old}/ianchor_#{old}_darwin_#{arch}.tar.gz"
    abort "Invalid #{arch} identity" unless urls == [expected] && shas.length == 1 && shas[0].match?(checksum)
  end
end

if (target.split('.').map(&:to_i) <=> old.split('.').map(&:to_i)) == -1
  warn "Skipping tap downgrade from #{old} to #{target}"
  exit 3
end
if old == target && arm_sha
  abort "Refusing to change source identity for existing version #{old}" if branches.empty?
  { 'on_arm' => arm_sha, 'on_intel' => amd_sha }.each do |name, sha|
    unless fields.call(branches[name], 'sha256')[0].downcase == sha.downcase
      abort "Refusing to change binary identity for existing version #{old}"
    end
  end
end
