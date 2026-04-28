async function a(r){if(!r)return!1;try{return await navigator.clipboard.writeText(r),!0}catch(e){return console.error("clipboard write failed",e),!1}}export{a as c};
