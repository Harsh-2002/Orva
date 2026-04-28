module.exports.handler = async function (event) {
  await new Promise((r) => setTimeout(r, 10000));
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ message: 'done' }),
  };
};
