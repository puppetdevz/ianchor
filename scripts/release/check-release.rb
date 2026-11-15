#!/usr/bin/env ruby
# frozen_string_literal: true

# Verifies the Release lookup response really describes the requested tag.
# Usage: check-release.rb RESPONSE TAG
require 'json'

file, tag = ARGV
response = File.read(file)
body = response.split(/\r?\n\r?\n/).drop(1).join("\n\n")
release = JSON.parse(body)
abort 'Unexpected or non-final Release response' unless release['tag_name'] == tag && release['draft'] == false && release['prerelease'] == false
