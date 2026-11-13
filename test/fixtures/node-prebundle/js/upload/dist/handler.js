var __getOwnPropNames = Object.getOwnPropertyNames;
var __commonJS = (cb, mod) => function __require() {
  return mod || (0, cb[__getOwnPropNames(cb)[0]])((mod = { exports: {} }).exports, mod), mod.exports;
};

// src/message.js
var require_message = __commonJS({
  "src/message.js"(exports2) {
    var { posix } = require("node:path");
    exports2.message = (name) => `Hello ${posix.basename(name)}`;
  }
});

// src/handler.js
var { message } = require_message();
var { kv } = require("orva");
exports.handler = async (event) => {
  const { name } = JSON.parse(event.body || "{}");
  await kv.put("prebundle:last", name);
  const saved = await kv.get("prebundle:last", null);
  return {
    statusCode: 201,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ revision: "bundle-A", language: "js", message: message(name), saved })
  };
};
