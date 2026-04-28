// Slow function — simulates external API call with 500ms delay.
module.exports.handler = async function (event) {
  await new Promise(r => setTimeout(r, 500));
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ message: 'slow response', delay_ms: 500 }),
  };
};
