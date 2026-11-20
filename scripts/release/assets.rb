#!/usr/bin/env ruby
# frozen_string_literal: true
require 'json'

command, path, argument = ARGV
case command
when 'release-id'
  response = File.binread(path)
  body = response.split(/\r?\n\r?\n/, 2)[1]
  release = JSON.parse(body || '')
  id = release['id']
  abort 'Invalid release id' unless id.is_a?(Integer) && id.positive?
  puts id
when 'state'
  pages = JSON.parse(File.read(path))
  abort 'Invalid asset listing' unless pages.is_a?(Array) && pages.all? { |page| page.is_a?(Array) }
  assets = pages.flatten(1)
  abort 'Invalid asset metadata' unless assets.all? { |asset| asset.is_a?(Hash) && asset['name'].is_a?(String) && asset['state'].is_a?(String) }
  matches = assets.select { |asset| asset['name'] == argument }
  abort 'Duplicate release asset name' if matches.length > 1
  if matches.empty?
    puts 'missing'
  else
    abort 'Release asset is not uploaded' unless matches[0]['state'] == 'uploaded'
    puts 'present'
  end
else
  abort 'usage: assets.rb release-id RESPONSE | state PAGES NAME'
end
