// CPU-bound function — fibonacci + prime sieve.
module.exports.handler = async function (event) {
  const body = typeof event.body === 'string' ? JSON.parse(event.body || '{}') : event.body || {};
  const n = Math.min(body.n || 30, 35); // cap at 35 to avoid timeout

  // Recursive fibonacci (intentionally slow)
  function fib(x) {
    if (x <= 1) return x;
    return fib(x - 1) + fib(x - 2);
  }

  const start = Date.now();
  const result = fib(n);
  const duration = Date.now() - start;

  // Also do a small prime sieve
  const limit = 10000;
  const sieve = new Array(limit).fill(true);
  sieve[0] = sieve[1] = false;
  for (let i = 2; i * i < limit; i++) {
    if (sieve[i]) for (let j = i * i; j < limit; j += i) sieve[j] = false;
  }
  const primeCount = sieve.filter(Boolean).length;

  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      fibonacci: { n, result },
      primes: { limit, count: primeCount },
      compute_ms: duration,
    }),
  };
};
