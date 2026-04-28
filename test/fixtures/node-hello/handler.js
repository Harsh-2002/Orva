module.exports.handler = async function (event) {
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      message: 'Hello from Node.js!',
      method: event.method,
      path: event.path,
    }),
  };
};
