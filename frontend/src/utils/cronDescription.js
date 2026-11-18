const descriptors = {
  '@yearly': 'Every year on January 1 at 00:00',
  '@annually': 'Every year on January 1 at 00:00',
  '@monthly': 'On day 1 of every month at 00:00',
  '@weekly': 'Every Sunday at 00:00',
  '@daily': 'Every day at 00:00',
  '@midnight': 'Every day at 00:00',
  '@hourly': 'Every hour at minute 0',
}

// Describe only exact whole-second intervals; leave the rest of Go's duration
// grammar and time-zone validation to the server, not a second parser in the UI.
function describeEvery(spec) {
  const duration = spec.slice('@every '.length)
  const literal = `Schedule expression: ${spec} (server validation required)`
  if (duration.length > 1024 || !/^(?:\d+[hms])+$/.test(duration)) return literal
  const units = { h: 3600n, m: 60n, s: 1n }
  let seconds = 0n
  for (const [, value, unit] of duration.matchAll(/(\d+)([hms])/g)) {
    seconds += BigInt(value) * units[unit]
  }
  if (seconds > 9223372036n) return literal
  if (seconds === 0n) return 'Every second (the minimum interval)'
  const parts = [
    [seconds / 3600n, 'hour'],
    [(seconds % 3600n) / 60n, 'minute'],
    [seconds % 60n, 'second'],
  ].filter(([n]) => n > 0n)
  if (parts.length === 1 && parts[0][0] === 1n) return `Every ${parts[0][1]}`
  return `Every ${parts.map(([n, word]) => `${n} ${word}${n === 1n ? '' : 's'}`).join(' ')}`
}

const plainField = (field, lo, hi) => {
  if (!/^\d{1,2}$/.test(field)) return false
  const n = Number(field)
  return n >= lo && n <= hi
}

// Advisory copy only. This does not validate or authorize saving a schedule.
export function humanizeCron(cron) {
  if (!cron) return 'Invalid expression'
  const spec = cron.trim()
  if (/^(?:CRON_TZ|TZ)=/.test(spec)) return `Schedule expression: ${cron} (server validation required)`
  if (Object.hasOwn(descriptors, spec)) return descriptors[spec]
  if (spec.startsWith('@every ')) return describeEvery(spec)
  if (spec.startsWith('@')) return `Unrecognized descriptor: ${spec}`

  const parts = cron.trim().split(/\s+/)
  if (parts.length !== 5) return 'Invalid format (use 5 fields)'

  const [min, hour, day, month, dow] = parts

  if (cron === '* * * * *') return 'Every minute'

  const atTime = `${hour.padStart(2, '0')}:${min.padStart(2, '0')}`

  if (plainField(min, 0, 59) && hour === '*' && day === '*' && month === '*' && dow === '*') {
    return `Every hour at minute ${min}`
  }
  if (plainField(min, 0, 59) && plainField(hour, 0, 23) && day === '*' && month === '*' && dow === '*') {
    return `Every day at ${atTime}`
  }
  if (plainField(min, 0, 59) && plainField(hour, 0, 23) && day === '*' && month === '*' && plainField(dow, 0, 6)) {
    const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
    return `Every ${days[Number(dow)]} at ${atTime}`
  }
  if (plainField(min, 0, 59) && plainField(hour, 0, 23) && plainField(day, 1, 31) && month === '*' && dow === '*') {
    return `On day ${day} of every month at ${atTime}`
  }

  return `Custom: ${cron}`
}
