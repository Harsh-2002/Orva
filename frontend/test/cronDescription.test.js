import { test } from 'node:test'
import assert from 'node:assert/strict'
import { humanizeCron } from '../src/utils/cronDescription.js'

test('describes exact whole-second Go intervals without a five-field error', () => {
  for (const [spec, label] of [
    ['@every 2s', 'Every 2 seconds'],
    ['@every 1h30m', 'Every 1 hour 30 minutes'],
    ['@every 1h', 'Every hour'],
    ['@every 60s', 'Every minute'],
    ['@every 0s', 'Every second (the minimum interval)'],
    ['@every 2m3s', 'Every 2 minutes 3 seconds'],
    ['@every 0002s', 'Every 2 seconds'],
  ]) assert.equal(humanizeCron(spec), label, spec)
})

test('uses only aliases recognized by the configured robfig Descriptor parser', () => {
  for (const [alias, fields] of [
    ['@yearly', '0 0 1 1 *'], ['@annually', '0 0 1 1 *'],
    ['@monthly', '0 0 1 * *'], ['@weekly', '0 0 * * 0'],
    ['@daily', '0 0 * * *'], ['@midnight', '0 0 * * *'], ['@hourly', '0 * * * *'],
  ]) {
    assert.doesNotMatch(humanizeCron(alias), /Invalid|Unrecognized|Descriptor:/)
    if (!['@yearly', '@annually'].includes(alias)) assert.equal(humanizeCron(alias), humanizeCron(fields), alias)
  }
  for (const spec of ['@reboot', '@DAILY', '@daily extra', '@every']) {
    assert.equal(humanizeCron(spec), `Unrecognized descriptor: ${spec}`)
  }
})

test('leaves complex durations, uncertain bounds and zone prefixes to server validation', () => {
  for (const spec of [
    '@every 1.5s', '@every .5h', '@every 1ms', '@every 1us', '@every 1µs',
    '@every 1μs', '@every 1ns', '@every -1h', '@every +2s', '@every 0',
    '@every 9223372037s', '@every 1d', '@every nonsense', '@every  2s',
    'CRON_TZ=Asia/Tokyo @daily', 'TZ=NotAZone 0 9 * * *',
  ]) assert.equal(humanizeCron(spec), `Schedule expression: ${spec} (server validation required)`, spec)
})

test('preserves five-field wording, including literal fallback for invalid or complex fields', () => {
  for (const [spec, label] of [
    ['', 'Invalid expression'],
    ['* *', 'Invalid format (use 5 fields)'],
    ['* * * * *', 'Every minute'],
    ['15 * * * *', 'Every hour at minute 15'],
    ['0 9 * * *', 'Every day at 09:00'],
    ['0 9 * * 1', 'Every Monday at 09:00'],
    ['0 9 15 * *', 'On day 15 of every month at 09:00'],
    ['0 9 * * 1-5', 'Custom: 0 9 * * 1-5'],
    ['*/2 * * * *', 'Custom: */2 * * * *'],
    ['99 99 * * *', 'Custom: 99 99 * * *'],
    ['0 9 * JAN MON', 'Custom: 0 9 * JAN MON'],
  ]) assert.equal(humanizeCron(spec), label, spec)
})
