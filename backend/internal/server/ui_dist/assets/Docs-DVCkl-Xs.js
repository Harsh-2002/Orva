import{C as yt,c as jt}from"./clipboard-CF_EA-U2.js";import{C as Ns,a as Ms,b as Ps}from"./aiPrompts-BUgkNEWn.js";import{z as Ds,o as Ls,L as Bs,a as be,b as t,q as Ve,m as Ye,f as m,F as He,n as Ue,d as E,ae as le,k as b,h as Oe,aQ as $s,t as R,g as js,r as Ie,p as Y,ap as Re,y as a,Q as Hs,I as Us,j as W,X as Ht,H as zs}from"./index-DmBqxyTa.js";import{C as wt}from"./copy-DsCyjuAc.js";import{G as Et}from"./globe-DUmm4wts.js";import{C as Ut}from"./chevron-right-DCr5GbWX.js";import{K as Je}from"./key-round-DaiFKJeb.js";import{V as Ks}from"./variable-CkzZmH8m.js";import{L as Fs}from"./lock-Dqm5_OOB.js";function Gs(l){return l&&l.__esModule&&Object.prototype.hasOwnProperty.call(l,"default")?l.default:l}var kt,zt;function qs(){if(zt)return kt;zt=1;function l(e){return e instanceof Map?e.clear=e.delete=e.set=function(){throw new Error("map is read-only")}:e instanceof Set&&(e.add=e.clear=e.delete=function(){throw new Error("set is read-only")}),Object.freeze(e),Object.getOwnPropertyNames(e).forEach(o=>{const r=e[o],y=typeof r;(y==="object"||y==="function")&&!Object.isFrozen(r)&&l(r)}),e}class I{constructor(o){o.data===void 0&&(o.data={}),this.data=o.data,this.isMatchIgnored=!1}ignoreMatch(){this.isMatchIgnored=!0}}function f(e){return e.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;").replace(/'/g,"&#x27;")}function _(e,...o){const r=Object.create(null);for(const y in e)r[y]=e[y];return o.forEach(function(y){for(const D in y)r[D]=y[D]}),r}const X="</span>",Z=e=>!!e.scope,se=(e,{prefix:o})=>{if(e.startsWith("language:"))return e.replace("language:","language-");if(e.includes(".")){const r=e.split(".");return[`${o}${r.shift()}`,...r.map((y,D)=>`${y}${"_".repeat(D+1)}`)].join(" ")}return`${o}${e}`};class M{constructor(o,r){this.buffer="",this.classPrefix=r.classPrefix,o.walk(this)}addText(o){this.buffer+=f(o)}openNode(o){if(!Z(o))return;const r=se(o.scope,{prefix:this.classPrefix});this.span(r)}closeNode(o){Z(o)&&(this.buffer+=X)}value(){return this.buffer}span(o){this.buffer+=`<span class="${o}">`}}const z=(e={})=>{const o={children:[]};return Object.assign(o,e),o};class K{constructor(){this.rootNode=z(),this.stack=[this.rootNode]}get top(){return this.stack[this.stack.length-1]}get root(){return this.rootNode}add(o){this.top.children.push(o)}openNode(o){const r=z({scope:o});this.add(r),this.stack.push(r)}closeNode(){if(this.stack.length>1)return this.stack.pop()}closeAllNodes(){for(;this.closeNode(););}toJSON(){return JSON.stringify(this.rootNode,null,4)}walk(o){return this.constructor._walk(o,this.rootNode)}static _walk(o,r){return typeof r=="string"?o.addText(r):r.children&&(o.openNode(r),r.children.forEach(y=>this._walk(o,y)),o.closeNode(r)),o}static _collapse(o){typeof o!="string"&&o.children&&(o.children.every(r=>typeof r=="string")?o.children=[o.children.join("")]:o.children.forEach(r=>{K._collapse(r)}))}}class te extends K{constructor(o){super(),this.options=o}addText(o){o!==""&&this.add(o)}startScope(o){this.openNode(o)}endScope(){this.closeNode()}__addSublanguage(o,r){const y=o.root;r&&(y.scope=`language:${r}`),this.add(y)}toHTML(){return new M(this,this.options).value()}finalize(){return this.closeAllNodes(),!0}}function F(e){return e?typeof e=="string"?e:e.source:null}function B(e){return $("(?=",e,")")}function de(e){return $("(?:",e,")*")}function J(e){return $("(?:",e,")?")}function $(...e){return e.map(r=>F(r)).join("")}function ue(e){const o=e[e.length-1];return typeof o=="object"&&o.constructor===Object?(e.splice(e.length-1,1),o):{}}function ne(...e){return"("+(ue(e).capture?"":"?:")+e.map(y=>F(y)).join("|")+")"}function pe(e){return new RegExp(e.toString()+"|").exec("").length-1}function _e(e,o){const r=e&&e.exec(o);return r&&r.index===0}const Se=/\[(?:[^\\\]]|\\.)*\]|\(\??|\\([1-9][0-9]*)|\\./;function ae(e,{joinWith:o}){let r=0;return e.map(y=>{r+=1;const D=r;let L=F(y),u="";for(;L.length>0;){const c=Se.exec(L);if(!c){u+=L;break}u+=L.substring(0,c.index),L=L.substring(c.index+c[0].length),c[0][0]==="\\"&&c[1]?u+="\\"+String(Number(c[1])+D):(u+=c[0],c[0]==="("&&r++)}return u}).map(y=>`(${y})`).join(o)}const fe=/\b\B/,Ne="[a-zA-Z]\\w*",Te="[a-zA-Z_]\\w*",Me="\\b\\d+(\\.\\d+)?",Pe="(-?)(\\b0[xX][a-fA-F0-9]+|(\\b\\d+(\\.\\d*)?|\\.\\d+)([eE][-+]?\\d+)?)",De="\\b(0b[01]+)",ze="!|!=|!==|%|%=|&|&&|&=|\\*|\\*=|\\+|\\+=|,|-|-=|/=|/|:|;|<<|<<=|<=|<|===|==|=|>>>=|>>=|>=|>>>|>>|>|\\?|\\[|\\{|\\(|\\^|\\^=|\\||\\|=|\\|\\||~",Ke=(e={})=>{const o=/^#![ ]*\//;return e.binary&&(e.begin=$(o,/.*\b/,e.binary,/\b.*/)),_({scope:"meta",begin:o,end:/$/,relevance:0,"on:begin":(r,y)=>{r.index!==0&&y.ignoreMatch()}},e)},me={begin:"\\\\[\\s\\S]",relevance:0},Fe={scope:"string",begin:"'",end:"'",illegal:"\\n",contains:[me]},Ge={scope:"string",begin:'"',end:'"',illegal:"\\n",contains:[me]},ve={begin:/\b(a|an|the|are|I'm|isn't|don't|doesn't|won't|but|just|should|pretty|simply|enough|gonna|going|wtf|so|such|will|you|your|they|like|more)\b/},j=function(e,o,r={}){const y=_({scope:"comment",begin:e,end:o,contains:[]},r);y.contains.push({scope:"doctag",begin:"[ ]*(?=(TODO|FIXME|NOTE|BUG|OPTIMIZE|HACK|XXX):)",end:/(TODO|FIXME|NOTE|BUG|OPTIMIZE|HACK|XXX):/,excludeBegin:!0,relevance:0});const D=ne("I","a","is","so","us","to","at","if","in","it","on",/[A-Za-z]+['](d|ve|re|ll|t|s|n)/,/[A-Za-z]+[-][a-z]+/,/[A-Za-z][a-z]{2,}/);return y.contains.push({begin:$(/[ ]+/,"(",D,/[.]?[:]?([.][ ]|[ ])/,"){3}")}),y},ye=j("//","$"),Q=j("/\\*","\\*/"),re=j("#","$"),he={scope:"number",begin:Me,relevance:0},qe={scope:"number",begin:Pe,relevance:0},ee={scope:"number",begin:De,relevance:0},it={scope:"regexp",begin:/\/(?=[^/\n]*\/)/,end:/\/[gimuy]*/,contains:[me,{begin:/\[/,end:/\]/,relevance:0,contains:[me]}]},Qe={scope:"title",begin:Ne,relevance:0},et={scope:"title",begin:Te,relevance:0},ct={begin:"\\.\\s*"+Te,relevance:0};var P=Object.freeze({__proto__:null,APOS_STRING_MODE:Fe,BACKSLASH_ESCAPE:me,BINARY_NUMBER_MODE:ee,BINARY_NUMBER_RE:De,COMMENT:j,C_BLOCK_COMMENT_MODE:Q,C_LINE_COMMENT_MODE:ye,C_NUMBER_MODE:qe,C_NUMBER_RE:Pe,END_SAME_AS_BEGIN:function(e){return Object.assign(e,{"on:begin":(o,r)=>{r.data._beginMatch=o[1]},"on:end":(o,r)=>{r.data._beginMatch!==o[1]&&r.ignoreMatch()}})},HASH_COMMENT_MODE:re,IDENT_RE:Ne,MATCH_NOTHING_RE:fe,METHOD_GUARD:ct,NUMBER_MODE:he,NUMBER_RE:Me,PHRASAL_WORDS_MODE:ve,QUOTE_STRING_MODE:Ge,REGEXP_MODE:it,RE_STARTERS_RE:ze,SHEBANG:Ke,TITLE_MODE:Qe,UNDERSCORE_IDENT_RE:Te,UNDERSCORE_TITLE_MODE:et});function ie(e,o){e.input[e.index-1]==="."&&o.ignoreMatch()}function Le(e,o){e.className!==void 0&&(e.scope=e.className,delete e.className)}function h(e,o){o&&e.beginKeywords&&(e.begin="\\b("+e.beginKeywords.split(" ").join("|")+")(?!\\.)(?=\\b|\\s)",e.__beforeBegin=ie,e.keywords=e.keywords||e.beginKeywords,delete e.beginKeywords,e.relevance===void 0&&(e.relevance=0))}function s(e,o){Array.isArray(e.illegal)&&(e.illegal=ne(...e.illegal))}function T(e,o){if(e.match){if(e.begin||e.end)throw new Error("begin & end are not supported with match");e.begin=e.match,delete e.match}}function d(e,o){e.relevance===void 0&&(e.relevance=1)}const N=(e,o)=>{if(!e.beforeMatch)return;if(e.starts)throw new Error("beforeMatch cannot be used with starts");const r=Object.assign({},e);Object.keys(e).forEach(y=>{delete e[y]}),e.keywords=r.keywords,e.begin=$(r.beforeMatch,B(r.begin)),e.starts={relevance:0,contains:[Object.assign(r,{endsParent:!0})]},e.relevance=0,delete r.beforeMatch},ce=["of","and","for","in","not","or","if","then","parent","list","value"],dt="keyword";function C(e,o,r=dt){const y=Object.create(null);return typeof e=="string"?D(r,e.split(" ")):Array.isArray(e)?D(r,e):Object.keys(e).forEach(function(L){Object.assign(y,C(e[L],o,L))}),y;function D(L,u){o&&(u=u.map(c=>c.toLowerCase())),u.forEach(function(c){const v=c.split("|");y[v[0]]=[L,ut(v[0],v[1])]})}}function ut(e,o){return o?Number(o):Be(e)?0:1}function Be(e){return ce.includes(e.toLowerCase())}const St={},$e=e=>{console.error(e)},Tt=(e,...o)=>{console.log(`WARN: ${e}`,...o)},We=(e,o)=>{St[`${e}/${o}`]||(console.log(`Deprecated as of ${e}. ${o}`),St[`${e}/${o}`]=!0)},tt=new Error;function xt(e,o,{key:r}){let y=0;const D=e[r],L={},u={};for(let c=1;c<=o.length;c++)u[c+y]=D[c],L[c+y]=!0,y+=pe(o[c-1]);e[r]=u,e[r]._emit=L,e[r]._multi=!0}function as(e){if(Array.isArray(e.begin)){if(e.skip||e.excludeBegin||e.returnBegin)throw $e("skip, excludeBegin, returnBegin not compatible with beginScope: {}"),tt;if(typeof e.beginScope!="object"||e.beginScope===null)throw $e("beginScope must be object"),tt;xt(e,e.begin,{key:"beginScope"}),e.begin=ae(e.begin,{joinWith:""})}}function rs(e){if(Array.isArray(e.end)){if(e.skip||e.excludeEnd||e.returnEnd)throw $e("skip, excludeEnd, returnEnd not compatible with endScope: {}"),tt;if(typeof e.endScope!="object"||e.endScope===null)throw $e("endScope must be object"),tt;xt(e,e.end,{key:"endScope"}),e.end=ae(e.end,{joinWith:""})}}function is(e){e.scope&&typeof e.scope=="object"&&e.scope!==null&&(e.beginScope=e.scope,delete e.scope)}function cs(e){is(e),typeof e.beginScope=="string"&&(e.beginScope={_wrap:e.beginScope}),typeof e.endScope=="string"&&(e.endScope={_wrap:e.endScope}),as(e),rs(e)}function ls(e){function o(u,c){return new RegExp(F(u),"m"+(e.case_insensitive?"i":"")+(e.unicodeRegex?"u":"")+(c?"g":""))}class r{constructor(){this.matchIndexes={},this.regexes=[],this.matchAt=1,this.position=0}addRule(c,v){v.position=this.position++,this.matchIndexes[this.matchAt]=v,this.regexes.push([v,c]),this.matchAt+=pe(c)+1}compile(){this.regexes.length===0&&(this.exec=()=>null);const c=this.regexes.map(v=>v[1]);this.matcherRe=o(ae(c,{joinWith:"|"}),!0),this.lastIndex=0}exec(c){this.matcherRe.lastIndex=this.lastIndex;const v=this.matcherRe.exec(c);if(!v)return null;const G=v.findIndex((Ze,ht)=>ht>0&&Ze!==void 0),H=this.matchIndexes[G];return v.splice(0,G),Object.assign(v,H)}}class y{constructor(){this.rules=[],this.multiRegexes=[],this.count=0,this.lastIndex=0,this.regexIndex=0}getMatcher(c){if(this.multiRegexes[c])return this.multiRegexes[c];const v=new r;return this.rules.slice(c).forEach(([G,H])=>v.addRule(G,H)),v.compile(),this.multiRegexes[c]=v,v}resumingScanAtSamePosition(){return this.regexIndex!==0}considerAll(){this.regexIndex=0}addRule(c,v){this.rules.push([c,v]),v.type==="begin"&&this.count++}exec(c){const v=this.getMatcher(this.regexIndex);v.lastIndex=this.lastIndex;let G=v.exec(c);if(this.resumingScanAtSamePosition()&&!(G&&G.index===this.lastIndex)){const H=this.getMatcher(0);H.lastIndex=this.lastIndex+1,G=H.exec(c)}return G&&(this.regexIndex+=G.position+1,this.regexIndex===this.count&&this.considerAll()),G}}function D(u){const c=new y;return u.contains.forEach(v=>c.addRule(v.begin,{rule:v,type:"begin"})),u.terminatorEnd&&c.addRule(u.terminatorEnd,{type:"end"}),u.illegal&&c.addRule(u.illegal,{type:"illegal"}),c}function L(u,c){const v=u;if(u.isCompiled)return v;[Le,T,cs,N].forEach(H=>H(u,c)),e.compilerExtensions.forEach(H=>H(u,c)),u.__beforeBegin=null,[h,s,d].forEach(H=>H(u,c)),u.isCompiled=!0;let G=null;return typeof u.keywords=="object"&&u.keywords.$pattern&&(u.keywords=Object.assign({},u.keywords),G=u.keywords.$pattern,delete u.keywords.$pattern),G=G||/\w+/,u.keywords&&(u.keywords=C(u.keywords,e.case_insensitive)),v.keywordPatternRe=o(G,!0),c&&(u.begin||(u.begin=/\B|\b/),v.beginRe=o(v.begin),!u.end&&!u.endsWithParent&&(u.end=/\B|\b/),u.end&&(v.endRe=o(v.end)),v.terminatorEnd=F(v.end)||"",u.endsWithParent&&c.terminatorEnd&&(v.terminatorEnd+=(u.end?"|":"")+c.terminatorEnd)),u.illegal&&(v.illegalRe=o(u.illegal)),u.contains||(u.contains=[]),u.contains=[].concat(...u.contains.map(function(H){return ds(H==="self"?u:H)})),u.contains.forEach(function(H){L(H,v)}),u.starts&&L(u.starts,c),v.matcher=D(v),v}if(e.compilerExtensions||(e.compilerExtensions=[]),e.contains&&e.contains.includes("self"))throw new Error("ERR: contains `self` is not supported at the top-level of a language.  See documentation.");return e.classNameAliases=_(e.classNameAliases||{}),L(e)}function Ct(e){return e?e.endsWithParent||Ct(e.starts):!1}function ds(e){return e.variants&&!e.cachedVariants&&(e.cachedVariants=e.variants.map(function(o){return _(e,{variants:null},o)})),e.cachedVariants?e.cachedVariants:Ct(e)?_(e,{starts:e.starts?_(e.starts):null}):Object.isFrozen(e)?_(e):e}var us="11.11.1";class ps extends Error{constructor(o,r){super(o),this.name="HTMLInjectionError",this.html=r}}const pt=f,At=_,Ot=Symbol("nomatch"),hs=7,It=function(e){const o=Object.create(null),r=Object.create(null),y=[];let D=!0;const L="Could not find the language '{}', did you forget to load/include a language module?",u={disableAutodetect:!0,name:"Plain text",contains:[]};let c={ignoreUnescapedHTML:!1,throwUnescapedHTML:!1,noHighlightRe:/^(no-?highlight)$/i,languageDetectRe:/\blang(?:uage)?-([\w-]+)\b/i,classPrefix:"hljs-",cssSelector:"pre code",languages:null,__emitter:te};function v(n){return c.noHighlightRe.test(n)}function G(n){let g=n.className+" ";g+=n.parentNode?n.parentNode.className:"";const S=c.languageDetectRe.exec(g);if(S){const A=Ce(S[1]);return A||(Tt(L.replace("{}",S[1])),Tt("Falling back to no-highlight mode for this block.",n)),A?S[1]:"no-highlight"}return g.split(/\s+/).find(A=>v(A)||Ce(A))}function H(n,g,S){let A="",U="";typeof g=="object"?(A=n,S=g.ignoreIllegals,U=g.language):(We("10.7.0","highlight(lang, code, ...args) has been deprecated."),We("10.7.0",`Please use highlight(code, options) instead.
https://github.com/highlightjs/highlight.js/issues/2277`),U=n,A=g),S===void 0&&(S=!0);const ge={code:A,language:U};ot("before:highlight",ge);const Ae=ge.result?ge.result:Ze(ge.language,ge.code,S);return Ae.code=ge.code,ot("after:highlight",Ae),Ae}function Ze(n,g,S,A){const U=Object.create(null);function ge(i,p){return i.keywords[p]}function Ae(){if(!w.keywords){q.addText(O);return}let i=0;w.keywordPatternRe.lastIndex=0;let p=w.keywordPatternRe.exec(O),k="";for(;p;){k+=O.substring(i,p.index);const x=Ee.case_insensitive?p[0].toLowerCase():p[0],V=ge(w,x);if(V){const[xe,Is]=V;if(q.addText(k),k="",U[x]=(U[x]||0)+1,U[x]<=hs&&(rt+=Is),xe.startsWith("_"))k+=p[0];else{const Rs=Ee.classNameAliases[xe]||xe;we(p[0],Rs)}}else k+=p[0];i=w.keywordPatternRe.lastIndex,p=w.keywordPatternRe.exec(O)}k+=O.substring(i),q.addText(k)}function nt(){if(O==="")return;let i=null;if(typeof w.subLanguage=="string"){if(!o[w.subLanguage]){q.addText(O);return}i=Ze(w.subLanguage,O,!0,$t[w.subLanguage]),$t[w.subLanguage]=i._top}else i=gt(O,w.subLanguage.length?w.subLanguage:null);w.relevance>0&&(rt+=i.relevance),q.__addSublanguage(i._emitter,i.language)}function oe(){w.subLanguage!=null?nt():Ae(),O=""}function we(i,p){i!==""&&(q.startScope(p),q.addText(i),q.endScope())}function Pt(i,p){let k=1;const x=p.length-1;for(;k<=x;){if(!i._emit[k]){k++;continue}const V=Ee.classNameAliases[i[k]]||i[k],xe=p[k];V?we(xe,V):(O=xe,Ae(),O=""),k++}}function Dt(i,p){return i.scope&&typeof i.scope=="string"&&q.openNode(Ee.classNameAliases[i.scope]||i.scope),i.beginScope&&(i.beginScope._wrap?(we(O,Ee.classNameAliases[i.beginScope._wrap]||i.beginScope._wrap),O=""):i.beginScope._multi&&(Pt(i.beginScope,p),O="")),w=Object.create(i,{parent:{value:w}}),w}function Lt(i,p,k){let x=_e(i.endRe,k);if(x){if(i["on:end"]){const V=new I(i);i["on:end"](p,V),V.isMatchIgnored&&(x=!1)}if(x){for(;i.endsParent&&i.parent;)i=i.parent;return i}}if(i.endsWithParent)return Lt(i.parent,p,k)}function Ts(i){return w.matcher.regexIndex===0?(O+=i[0],1):(vt=!0,0)}function xs(i){const p=i[0],k=i.rule,x=new I(k),V=[k.__beforeBegin,k["on:begin"]];for(const xe of V)if(xe&&(xe(i,x),x.isMatchIgnored))return Ts(p);return k.skip?O+=p:(k.excludeBegin&&(O+=p),oe(),!k.returnBegin&&!k.excludeBegin&&(O=p)),Dt(k,i),k.returnBegin?0:p.length}function Cs(i){const p=i[0],k=g.substring(i.index),x=Lt(w,i,k);if(!x)return Ot;const V=w;w.endScope&&w.endScope._wrap?(oe(),we(p,w.endScope._wrap)):w.endScope&&w.endScope._multi?(oe(),Pt(w.endScope,i)):V.skip?O+=p:(V.returnEnd||V.excludeEnd||(O+=p),oe(),V.excludeEnd&&(O=p));do w.scope&&q.closeNode(),!w.skip&&!w.subLanguage&&(rt+=w.relevance),w=w.parent;while(w!==x.parent);return x.starts&&Dt(x.starts,i),V.returnEnd?0:p.length}function As(){const i=[];for(let p=w;p!==Ee;p=p.parent)p.scope&&i.unshift(p.scope);i.forEach(p=>q.openNode(p))}let at={};function Bt(i,p){const k=p&&p[0];if(O+=i,k==null)return oe(),0;if(at.type==="begin"&&p.type==="end"&&at.index===p.index&&k===""){if(O+=g.slice(p.index,p.index+1),!D){const x=new Error(`0 width match regex (${n})`);throw x.languageName=n,x.badRule=at.rule,x}return 1}if(at=p,p.type==="begin")return xs(p);if(p.type==="illegal"&&!S){const x=new Error('Illegal lexeme "'+k+'" for mode "'+(w.scope||"<unnamed>")+'"');throw x.mode=w,x}else if(p.type==="end"){const x=Cs(p);if(x!==Ot)return x}if(p.type==="illegal"&&k==="")return O+=`
`,1;if(mt>1e5&&mt>p.index*3)throw new Error("potential infinite loop, way more iterations than matches");return O+=k,k.length}const Ee=Ce(n);if(!Ee)throw $e(L.replace("{}",n)),new Error('Unknown language: "'+n+'"');const Os=ls(Ee);let ft="",w=A||Os;const $t={},q=new c.__emitter(c);As();let O="",rt=0,je=0,mt=0,vt=!1;try{if(Ee.__emitTokens)Ee.__emitTokens(g,q);else{for(w.matcher.considerAll();;){mt++,vt?vt=!1:w.matcher.considerAll(),w.matcher.lastIndex=je;const i=w.matcher.exec(g);if(!i)break;const p=g.substring(je,i.index),k=Bt(p,i);je=i.index+k}Bt(g.substring(je))}return q.finalize(),ft=q.toHTML(),{language:n,value:ft,relevance:rt,illegal:!1,_emitter:q,_top:w}}catch(i){if(i.message&&i.message.includes("Illegal"))return{language:n,value:pt(g),illegal:!0,relevance:0,_illegalBy:{message:i.message,index:je,context:g.slice(je-100,je+100),mode:i.mode,resultSoFar:ft},_emitter:q};if(D)return{language:n,value:pt(g),illegal:!1,relevance:0,errorRaised:i,_emitter:q,_top:w};throw i}}function ht(n){const g={value:pt(n),illegal:!1,relevance:0,_top:u,_emitter:new c.__emitter(c)};return g._emitter.addText(n),g}function gt(n,g){g=g||c.languages||Object.keys(o);const S=ht(n),A=g.filter(Ce).filter(Mt).map(oe=>Ze(oe,n,!1));A.unshift(S);const U=A.sort((oe,we)=>{if(oe.relevance!==we.relevance)return we.relevance-oe.relevance;if(oe.language&&we.language){if(Ce(oe.language).supersetOf===we.language)return 1;if(Ce(we.language).supersetOf===oe.language)return-1}return 0}),[ge,Ae]=U,nt=ge;return nt.secondBest=Ae,nt}function gs(n,g,S){const A=g&&r[g]||S;n.classList.add("hljs"),n.classList.add(`language-${A}`)}function bt(n){let g=null;const S=G(n);if(v(S))return;if(ot("before:highlightElement",{el:n,language:S}),n.dataset.highlighted){console.log("Element previously highlighted. To highlight again, first unset `dataset.highlighted`.",n);return}if(n.children.length>0&&(c.ignoreUnescapedHTML||(console.warn("One of your code blocks includes unescaped HTML. This is a potentially serious security risk."),console.warn("https://github.com/highlightjs/highlight.js/wiki/security"),console.warn("The element with unescaped HTML:"),console.warn(n)),c.throwUnescapedHTML))throw new ps("One of your code blocks includes unescaped HTML.",n.innerHTML);g=n;const A=g.textContent,U=S?H(A,{language:S,ignoreIllegals:!0}):gt(A);n.innerHTML=U.value,n.dataset.highlighted="yes",gs(n,S,U.language),n.result={language:U.language,re:U.relevance,relevance:U.relevance},U.secondBest&&(n.secondBest={language:U.secondBest.language,relevance:U.secondBest.relevance}),ot("after:highlightElement",{el:n,result:U,text:A})}function bs(n){c=At(c,n)}const fs=()=>{st(),We("10.6.0","initHighlighting() deprecated.  Use highlightAll() now.")};function ms(){st(),We("10.6.0","initHighlightingOnLoad() deprecated.  Use highlightAll() now.")}let Rt=!1;function st(){function n(){st()}if(document.readyState==="loading"){Rt||window.addEventListener("DOMContentLoaded",n,!1),Rt=!0;return}document.querySelectorAll(c.cssSelector).forEach(bt)}function vs(n,g){let S=null;try{S=g(e)}catch(A){if($e("Language definition for '{}' could not be registered.".replace("{}",n)),D)$e(A);else throw A;S=u}S.name||(S.name=n),o[n]=S,S.rawDefinition=g.bind(null,e),S.aliases&&Nt(S.aliases,{languageName:n})}function ys(n){delete o[n];for(const g of Object.keys(r))r[g]===n&&delete r[g]}function ws(){return Object.keys(o)}function Ce(n){return n=(n||"").toLowerCase(),o[n]||o[r[n]]}function Nt(n,{languageName:g}){typeof n=="string"&&(n=[n]),n.forEach(S=>{r[S.toLowerCase()]=g})}function Mt(n){const g=Ce(n);return g&&!g.disableAutodetect}function Es(n){n["before:highlightBlock"]&&!n["before:highlightElement"]&&(n["before:highlightElement"]=g=>{n["before:highlightBlock"](Object.assign({block:g.el},g))}),n["after:highlightBlock"]&&!n["after:highlightElement"]&&(n["after:highlightElement"]=g=>{n["after:highlightBlock"](Object.assign({block:g.el},g))})}function ks(n){Es(n),y.push(n)}function _s(n){const g=y.indexOf(n);g!==-1&&y.splice(g,1)}function ot(n,g){const S=n;y.forEach(function(A){A[S]&&A[S](g)})}function Ss(n){return We("10.7.0","highlightBlock will be removed entirely in v12.0"),We("10.7.0","Please use highlightElement now."),bt(n)}Object.assign(e,{highlight:H,highlightAuto:gt,highlightAll:st,highlightElement:bt,highlightBlock:Ss,configure:bs,initHighlighting:fs,initHighlightingOnLoad:ms,registerLanguage:vs,unregisterLanguage:ys,listLanguages:ws,getLanguage:Ce,registerAliases:Nt,autoDetection:Mt,inherit:At,addPlugin:ks,removePlugin:_s}),e.debugMode=function(){D=!1},e.safeMode=function(){D=!0},e.versionString=us,e.regex={concat:$,lookahead:B,either:ne,optional:J,anyNumberOfTimes:de};for(const n in P)typeof P[n]=="object"&&l(P[n]);return Object.assign(e,P),e},Xe=It({});return Xe.newInstance=()=>It({}),kt=Xe,Xe.HighlightJS=Xe,Xe.default=Xe,kt}var Ws=qs();const ke=Gs(Ws);function Xs(l){const I=l.regex,f=new RegExp("[\\p{XID_Start}_]\\p{XID_Continue}*","u"),_=["and","as","assert","async","await","break","case","class","continue","def","del","elif","else","except","finally","for","from","global","if","import","in","is","lambda","match","nonlocal|10","not","or","pass","raise","return","try","while","with","yield"],M={$pattern:/[A-Za-z]\w+|__\w+__/,keyword:_,built_in:["__import__","abs","all","any","ascii","bin","bool","breakpoint","bytearray","bytes","callable","chr","classmethod","compile","complex","delattr","dict","dir","divmod","enumerate","eval","exec","filter","float","format","frozenset","getattr","globals","hasattr","hash","help","hex","id","input","int","isinstance","issubclass","iter","len","list","locals","map","max","memoryview","min","next","object","oct","open","ord","pow","print","property","range","repr","reversed","round","set","setattr","slice","sorted","staticmethod","str","sum","super","tuple","type","vars","zip"],literal:["__debug__","Ellipsis","False","None","NotImplemented","True"],type:["Any","Callable","Coroutine","Dict","List","Literal","Generic","Optional","Sequence","Set","Tuple","Type","Union"]},z={className:"meta",begin:/^(>>>|\.\.\.) /},K={className:"subst",begin:/\{/,end:/\}/,keywords:M,illegal:/#/},te={begin:/\{\{/,relevance:0},F={className:"string",contains:[l.BACKSLASH_ESCAPE],variants:[{begin:/([uU]|[bB]|[rR]|[bB][rR]|[rR][bB])?'''/,end:/'''/,contains:[l.BACKSLASH_ESCAPE,z],relevance:10},{begin:/([uU]|[bB]|[rR]|[bB][rR]|[rR][bB])?"""/,end:/"""/,contains:[l.BACKSLASH_ESCAPE,z],relevance:10},{begin:/([fF][rR]|[rR][fF]|[fF])'''/,end:/'''/,contains:[l.BACKSLASH_ESCAPE,z,te,K]},{begin:/([fF][rR]|[rR][fF]|[fF])"""/,end:/"""/,contains:[l.BACKSLASH_ESCAPE,z,te,K]},{begin:/([uU]|[rR])'/,end:/'/,relevance:10},{begin:/([uU]|[rR])"/,end:/"/,relevance:10},{begin:/([bB]|[bB][rR]|[rR][bB])'/,end:/'/},{begin:/([bB]|[bB][rR]|[rR][bB])"/,end:/"/},{begin:/([fF][rR]|[rR][fF]|[fF])'/,end:/'/,contains:[l.BACKSLASH_ESCAPE,te,K]},{begin:/([fF][rR]|[rR][fF]|[fF])"/,end:/"/,contains:[l.BACKSLASH_ESCAPE,te,K]},l.APOS_STRING_MODE,l.QUOTE_STRING_MODE]},B="[0-9](_?[0-9])*",de=`(\\b(${B}))?\\.(${B})|\\b(${B})\\.`,J=`\\b|${_.join("|")}`,$={className:"number",relevance:0,variants:[{begin:`(\\b(${B})|(${de}))[eE][+-]?(${B})[jJ]?(?=${J})`},{begin:`(${de})[jJ]?`},{begin:`\\b([1-9](_?[0-9])*|0+(_?0)*)[lLjJ]?(?=${J})`},{begin:`\\b0[bB](_?[01])+[lL]?(?=${J})`},{begin:`\\b0[oO](_?[0-7])+[lL]?(?=${J})`},{begin:`\\b0[xX](_?[0-9a-fA-F])+[lL]?(?=${J})`},{begin:`\\b(${B})[jJ](?=${J})`}]},ue={className:"comment",begin:I.lookahead(/# type:/),end:/$/,keywords:M,contains:[{begin:/# type:/},{begin:/#/,end:/\b\B/,endsWithParent:!0}]},ne={className:"params",variants:[{className:"",begin:/\(\s*\)/,skip:!0},{begin:/\(/,end:/\)/,excludeBegin:!0,excludeEnd:!0,keywords:M,contains:["self",z,$,F,l.HASH_COMMENT_MODE]}]};return K.contains=[F,$,z],{name:"Python",aliases:["py","gyp","ipython"],unicodeRegex:!0,keywords:M,illegal:/(<\/|\?)|=>/,contains:[z,$,{scope:"variable.language",match:/\bself\b/},{beginKeywords:"if",relevance:0},{match:/\bor\b/,scope:"keyword"},F,ue,l.HASH_COMMENT_MODE,{match:[/\bdef/,/\s+/,f],scope:{1:"keyword",3:"title.function"},contains:[ne]},{variants:[{match:[/\bclass/,/\s+/,f,/\s*/,/\(\s*/,f,/\s*\)/]},{match:[/\bclass/,/\s+/,f]}],scope:{1:"keyword",3:"title.class",6:"title.class.inherited"}},{className:"meta",begin:/^[\t ]*@/,end:/(?=#)|$/,contains:[$,ne,F]}]}}const Kt="[A-Za-z$_][0-9A-Za-z$_]*",Vs=["as","in","of","if","for","while","finally","var","new","function","do","return","void","else","break","catch","instanceof","with","throw","case","default","try","switch","continue","typeof","delete","let","yield","const","class","debugger","async","await","static","import","from","export","extends","using"],Ys=["true","false","null","undefined","NaN","Infinity"],ss=["Object","Function","Boolean","Symbol","Math","Date","Number","BigInt","String","RegExp","Array","Float32Array","Float64Array","Int8Array","Uint8Array","Uint8ClampedArray","Int16Array","Int32Array","Uint16Array","Uint32Array","BigInt64Array","BigUint64Array","Set","Map","WeakSet","WeakMap","ArrayBuffer","SharedArrayBuffer","Atomics","DataView","JSON","Promise","Generator","GeneratorFunction","AsyncFunction","Reflect","Proxy","Intl","WebAssembly"],os=["Error","EvalError","InternalError","RangeError","ReferenceError","SyntaxError","TypeError","URIError"],ns=["setInterval","setTimeout","clearInterval","clearTimeout","require","exports","eval","isFinite","isNaN","parseFloat","parseInt","decodeURI","decodeURIComponent","encodeURI","encodeURIComponent","escape","unescape"],Zs=["arguments","this","super","console","window","document","localStorage","sessionStorage","module","global"],Js=[].concat(ns,ss,os);function Ft(l){const I=l.regex,f=(j,{after:ye})=>{const Q="</"+j[0].slice(1);return j.input.indexOf(Q,ye)!==-1},_=Kt,X={begin:"<>",end:"</>"},Z=/<[A-Za-z0-9\\._:-]+\s*\/>/,se={begin:/<[A-Za-z0-9\\._:-]+/,end:/\/[A-Za-z0-9\\._:-]+>|\/>/,isTrulyOpeningTag:(j,ye)=>{const Q=j[0].length+j.index,re=j.input[Q];if(re==="<"||re===","){ye.ignoreMatch();return}re===">"&&(f(j,{after:Q})||ye.ignoreMatch());let he;const qe=j.input.substring(Q);if(he=qe.match(/^\s*=/)){ye.ignoreMatch();return}if((he=qe.match(/^\s+extends\s+/))&&he.index===0){ye.ignoreMatch();return}}},M={$pattern:Kt,keyword:Vs,literal:Ys,built_in:Js,"variable.language":Zs},z="[0-9](_?[0-9])*",K=`\\.(${z})`,te="0|[1-9](_?[0-9])*|0[0-7]*[89][0-9]*",F={className:"number",variants:[{begin:`(\\b(${te})((${K})|\\.)?|(${K}))[eE][+-]?(${z})\\b`},{begin:`\\b(${te})\\b((${K})\\b|\\.)?|(${K})\\b`},{begin:"\\b(0|[1-9](_?[0-9])*)n\\b"},{begin:"\\b0[xX][0-9a-fA-F](_?[0-9a-fA-F])*n?\\b"},{begin:"\\b0[bB][0-1](_?[0-1])*n?\\b"},{begin:"\\b0[oO][0-7](_?[0-7])*n?\\b"},{begin:"\\b0[0-7]+n?\\b"}],relevance:0},B={className:"subst",begin:"\\$\\{",end:"\\}",keywords:M,contains:[]},de={begin:".?html`",end:"",starts:{end:"`",returnEnd:!1,contains:[l.BACKSLASH_ESCAPE,B],subLanguage:"xml"}},J={begin:".?css`",end:"",starts:{end:"`",returnEnd:!1,contains:[l.BACKSLASH_ESCAPE,B],subLanguage:"css"}},$={begin:".?gql`",end:"",starts:{end:"`",returnEnd:!1,contains:[l.BACKSLASH_ESCAPE,B],subLanguage:"graphql"}},ue={className:"string",begin:"`",end:"`",contains:[l.BACKSLASH_ESCAPE,B]},pe={className:"comment",variants:[l.COMMENT(/\/\*\*(?!\/)/,"\\*/",{relevance:0,contains:[{begin:"(?=@[A-Za-z]+)",relevance:0,contains:[{className:"doctag",begin:"@[A-Za-z]+"},{className:"type",begin:"\\{",end:"\\}",excludeEnd:!0,excludeBegin:!0,relevance:0},{className:"variable",begin:_+"(?=\\s*(-)|$)",endsParent:!0,relevance:0},{begin:/(?=[^\n])\s/,relevance:0}]}]}),l.C_BLOCK_COMMENT_MODE,l.C_LINE_COMMENT_MODE]},_e=[l.APOS_STRING_MODE,l.QUOTE_STRING_MODE,de,J,$,ue,{match:/\$\d+/},F];B.contains=_e.concat({begin:/\{/,end:/\}/,keywords:M,contains:["self"].concat(_e)});const Se=[].concat(pe,B.contains),ae=Se.concat([{begin:/(\s*)\(/,end:/\)/,keywords:M,contains:["self"].concat(Se)}]),fe={className:"params",begin:/(\s*)\(/,end:/\)/,excludeBegin:!0,excludeEnd:!0,keywords:M,contains:ae},Ne={variants:[{match:[/class/,/\s+/,_,/\s+/,/extends/,/\s+/,I.concat(_,"(",I.concat(/\./,_),")*")],scope:{1:"keyword",3:"title.class",5:"keyword",7:"title.class.inherited"}},{match:[/class/,/\s+/,_],scope:{1:"keyword",3:"title.class"}}]},Te={relevance:0,match:I.either(/\bJSON/,/\b[A-Z][a-z]+([A-Z][a-z]*|\d)*/,/\b[A-Z]{2,}([A-Z][a-z]+|\d)+([A-Z][a-z]*)*/,/\b[A-Z]{2,}[a-z]+([A-Z][a-z]+|\d)*([A-Z][a-z]*)*/),className:"title.class",keywords:{_:[...ss,...os]}},Me={label:"use_strict",className:"meta",relevance:10,begin:/^\s*['"]use (strict|asm)['"]/},Pe={variants:[{match:[/function/,/\s+/,_,/(?=\s*\()/]},{match:[/function/,/\s*(?=\()/]}],className:{1:"keyword",3:"title.function"},label:"func.def",contains:[fe],illegal:/%/},De={relevance:0,match:/\b[A-Z][A-Z_0-9]+\b/,className:"variable.constant"};function ze(j){return I.concat("(?!",j.join("|"),")")}const Ke={match:I.concat(/\b/,ze([...ns,"super","import"].map(j=>`${j}\\s*\\(`)),_,I.lookahead(/\s*\(/)),className:"title.function",relevance:0},me={begin:I.concat(/\./,I.lookahead(I.concat(_,/(?![0-9A-Za-z$_(])/))),end:_,excludeBegin:!0,keywords:"prototype",className:"property",relevance:0},Fe={match:[/get|set/,/\s+/,_,/(?=\()/],className:{1:"keyword",3:"title.function"},contains:[{begin:/\(\)/},fe]},Ge="(\\([^()]*(\\([^()]*(\\([^()]*\\)[^()]*)*\\)[^()]*)*\\)|"+l.UNDERSCORE_IDENT_RE+")\\s*=>",ve={match:[/const|var|let/,/\s+/,_,/\s*/,/=\s*/,/(async\s*)?/,I.lookahead(Ge)],keywords:"async",className:{1:"keyword",3:"title.function"},contains:[fe]};return{name:"JavaScript",aliases:["js","jsx","mjs","cjs"],keywords:M,exports:{PARAMS_CONTAINS:ae,CLASS_REFERENCE:Te},illegal:/#(?![$_A-z])/,contains:[l.SHEBANG({label:"shebang",binary:"node",relevance:5}),Me,l.APOS_STRING_MODE,l.QUOTE_STRING_MODE,de,J,$,ue,pe,{match:/\$\d+/},F,Te,{scope:"attr",match:_+I.lookahead(":"),relevance:0},ve,{begin:"("+l.RE_STARTERS_RE+"|\\b(case|return|throw)\\b)\\s*",keywords:"return throw case",relevance:0,contains:[pe,l.REGEXP_MODE,{className:"function",begin:Ge,returnBegin:!0,end:"\\s*=>",contains:[{className:"params",variants:[{begin:l.UNDERSCORE_IDENT_RE,relevance:0},{className:null,begin:/\(\s*\)/,skip:!0},{begin:/(\s*)\(/,end:/\)/,excludeBegin:!0,excludeEnd:!0,keywords:M,contains:ae}]}]},{begin:/,/,relevance:0},{match:/\s+/,relevance:0},{variants:[{begin:X.begin,end:X.end},{match:Z},{begin:se.begin,"on:begin":se.isTrulyOpeningTag,end:se.end}],subLanguage:"xml",contains:[{begin:se.begin,end:se.end,skip:!0,contains:["self"]}]}]},Pe,{beginKeywords:"while if switch catch for"},{begin:"\\b(?!function)"+l.UNDERSCORE_IDENT_RE+"\\([^()]*(\\([^()]*(\\([^()]*\\)[^()]*)*\\)[^()]*)*\\)\\s*\\{",returnBegin:!0,label:"func.def",contains:[fe,l.inherit(l.TITLE_MODE,{begin:_,className:"title.function"})]},{match:/\.\.\./,relevance:0},me,{match:"\\$"+_,relevance:0},{match:[/\bconstructor(?=\s*\()/],className:{1:"title.function"},contains:[fe]},Ke,De,Ne,Fe,{match:/\$[(.]/}]}}function Qs(l){const I={className:"attr",begin:/"(\\.|[^\\"\r\n])*"(?=\s*:)/,relevance:1.01},f={match:/[{}[\],:]/,className:"punctuation",relevance:0},_=["true","false","null"],X={scope:"literal",beginKeywords:_.join(" ")};return{name:"JSON",aliases:["jsonc"],keywords:{literal:_},contains:[I,f,l.QUOTE_STRING_MODE,X,l.C_NUMBER_MODE,l.C_LINE_COMMENT_MODE,l.C_BLOCK_COMMENT_MODE],illegal:"\\S"}}function _t(l){const I=l.regex,f={},_={begin:/\$\{/,end:/\}/,contains:["self",{begin:/:-/,contains:[f]}]};Object.assign(f,{className:"variable",variants:[{begin:I.concat(/\$[\w\d#@][\w\d_]*/,"(?![\\w\\d])(?![$])")},_]});const X={className:"subst",begin:/\$\(/,end:/\)/,contains:[l.BACKSLASH_ESCAPE]},Z=l.inherit(l.COMMENT(),{match:[/(^|\s)/,/#.*$/],scope:{2:"comment"}}),se={begin:/<<-?\s*(?=\w+)/,starts:{contains:[l.END_SAME_AS_BEGIN({begin:/(\w+)/,end:/(\w+)/,className:"string"})]}},M={className:"string",begin:/"/,end:/"/,contains:[l.BACKSLASH_ESCAPE,f,X]};X.contains.push(M);const z={match:/\\"/},K={className:"string",begin:/'/,end:/'/},te={match:/\\'/},F={begin:/\$?\(\(/,end:/\)\)/,contains:[{begin:/\d+#[0-9a-f]+/,className:"number"},l.NUMBER_MODE,f]},B=["fish","bash","zsh","sh","csh","ksh","tcsh","dash","scsh"],de=l.SHEBANG({binary:`(${B.join("|")})`,relevance:10}),J={className:"function",begin:/\w[\w\d_]*\s*\(\s*\)\s*\{/,returnBegin:!0,contains:[l.inherit(l.TITLE_MODE,{begin:/\w[\w\d_]*/})],relevance:0},$=["if","then","else","elif","fi","time","for","while","until","in","do","done","case","esac","coproc","function","select"],ue=["true","false"],ne={match:/(\/[a-z._-]+)+/},pe=["break","cd","continue","eval","exec","exit","export","getopts","hash","pwd","readonly","return","shift","test","times","trap","umask","unset"],_e=["alias","bind","builtin","caller","command","declare","echo","enable","help","let","local","logout","mapfile","printf","read","readarray","source","sudo","type","typeset","ulimit","unalias"],Se=["autoload","bg","bindkey","bye","cap","chdir","clone","comparguments","compcall","compctl","compdescribe","compfiles","compgroups","compquote","comptags","comptry","compvalues","dirs","disable","disown","echotc","echoti","emulate","fc","fg","float","functions","getcap","getln","history","integer","jobs","kill","limit","log","noglob","popd","print","pushd","pushln","rehash","sched","setcap","setopt","stat","suspend","ttyctl","unfunction","unhash","unlimit","unsetopt","vared","wait","whence","where","which","zcompile","zformat","zftp","zle","zmodload","zparseopts","zprof","zpty","zregexparse","zsocket","zstyle","ztcp"],ae=["chcon","chgrp","chown","chmod","cp","dd","df","dir","dircolors","ln","ls","mkdir","mkfifo","mknod","mktemp","mv","realpath","rm","rmdir","shred","sync","touch","truncate","vdir","b2sum","base32","base64","cat","cksum","comm","csplit","cut","expand","fmt","fold","head","join","md5sum","nl","numfmt","od","paste","ptx","pr","sha1sum","sha224sum","sha256sum","sha384sum","sha512sum","shuf","sort","split","sum","tac","tail","tr","tsort","unexpand","uniq","wc","arch","basename","chroot","date","dirname","du","echo","env","expr","factor","groups","hostid","id","link","logname","nice","nohup","nproc","pathchk","pinky","printenv","printf","pwd","readlink","runcon","seq","sleep","stat","stdbuf","stty","tee","test","timeout","tty","uname","unlink","uptime","users","who","whoami","yes"];return{name:"Bash",aliases:["sh","zsh"],keywords:{$pattern:/\b[a-z][a-z0-9._-]+\b/,keyword:$,literal:ue,built_in:[...pe,..._e,"set","shopt",...Se,...ae]},contains:[de,l.SHEBANG(),J,F,Z,se,ne,M,z,K,te,f]}}function eo(l){const I=l.regex,f="HTTP/([32]|1\\.[01])",_=/[A-Za-z][A-Za-z0-9-]*/,X={className:"attribute",begin:I.concat("^",_,"(?=\\:\\s)"),starts:{contains:[{className:"punctuation",begin:/: /,relevance:0,starts:{end:"$",relevance:0}}]}},Z=[X,{begin:"\\n\\n",starts:{subLanguage:[],endsWithParent:!0}}];return{name:"HTTP",aliases:["https"],illegal:/\S/,contains:[{begin:"^(?="+f+" \\d{3})",end:/$/,contains:[{className:"meta",begin:f},{className:"number",begin:"\\b\\d{3}\\b"}],starts:{end:/\b\B/,illegal:/\S/,contains:Z}},{begin:"(?=^[A-Z]+ (.*?) "+f+"$)",end:/$/,contains:[{className:"string",begin:" ",end:" ",excludeBegin:!0,excludeEnd:!0},{className:"meta",begin:f},{className:"keyword",begin:"[A-Z]+"}],starts:{end:/\b\B/,illegal:/\S/,contains:Z}},l.inherit(X,{relevance:0})]}}const to={class:"space-y-12 pb-16"},so={class:"docs-hero"},oo={class:"docs-hero-content"},no={class:"docs-hero-row"},ao={class:"docs-hero-actions"},ro=["title","aria-label"],io={class:"docs-hero-toc","aria-label":"Jump to docs section"},co=["href"],lo={class:"docs-hero-toc-num"},uo={id:"handler",class:"space-y-5 scroll-mt-6"},po={class:"doc-table-wrap"},ho={class:"doc-table"},go={class:"doc-cell-key"},bo={class:"doc-cell-mono"},fo={class:"doc-cell-mono hidden sm:table-cell"},mo={class:"doc-cell-mono hidden md:table-cell"},vo={id:"deploy",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},yo={class:"grid grid-cols-1 lg:grid-cols-2 gap-3"},wo={class:"space-y-2"},Eo={class:"space-y-2"},ko={class:"space-y-2"},_o={id:"config",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},So={class:"doc-table-wrap"},To={class:"doc-table"},xo={class:"doc-cell-key whitespace-nowrap"},Co={class:"doc-cell-mono hidden sm:table-cell whitespace-nowrap"},Ao={class:"doc-cell-body"},Oo={class:"space-y-2"},Io={class:"doc-details group"},Ro={class:"doc-details-summary"},No={class:"doc-details-body"},Mo={id:"sdk",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Po={class:"space-y-2"},Do={class:"space-y-2"},Lo={class:"space-y-2"},Bo={id:"schedules",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},$o={class:"doc-section-head"},jo={class:"doc-lede"},Ho={id:"webhooks",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Uo={class:"doc-section-head"},zo={class:"doc-lede"},Ko={class:"doc-table-wrap"},Fo={class:"doc-table"},Go={class:"doc-cell-key whitespace-nowrap"},qo={class:"doc-cell-body"},Wo={class:"space-y-2"},Xo={id:"mcp",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Vo={class:"grid grid-cols-1 md:grid-cols-3 gap-3"},Yo={class:"doc-card"},Zo={class:"doc-card-body"},Jo={class:"doc-chip break-all"},Qo={class:"doc-token-bar"},en={class:"flex items-center gap-2 min-w-0 flex-1"},tn={key:0,class:"text-sm text-foreground-muted truncate"},sn={key:1,class:"text-sm text-success truncate"},on={class:"doc-chip"},nn=["disabled"],an={class:"doc-details group"},rn={class:"doc-details-summary"},cn={class:"doc-details-body space-y-4"},ln={id:"generate",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},dn={class:"ai-prompt-actions"},un={key:0,class:"prompt-collapse-fade","aria-hidden":"true"},pn=["aria-expanded"],hn={id:"tracing",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},gn={class:"doc-table-wrap"},bn={class:"doc-table"},fn={class:"doc-cell-key whitespace-nowrap"},mn={class:"doc-cell-body"},vn={id:"errors",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},yn={class:"doc-table-wrap"},wn={class:"doc-table"},En={class:"doc-cell-key whitespace-nowrap"},kn={class:"doc-cell-body"},_n={id:"cli",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Sn={class:"doc-prose"},Tn={class:"doc-table-wrap"},xn={class:"doc-table"},Cn={class:"doc-cell-key whitespace-nowrap"},An={class:"doc-cell-mono"},On={class:"doc-cell-body hidden md:table-cell"},In={class:"space-y-2"},Rn={class:"space-y-2"},Nn={class:"space-y-2"},Mn={class:"space-y-2"},Pn={class:"space-y-2"},Gt=`# Available inside every running function — refresh per-invocation:
ORVA_TRACE_ID=tr_3e39f6991c66f140577c6021da7dd13b   # one per causal chain
ORVA_SPAN_ID=sp_4ceba57f6b1c982e                    # this execution

# Python:        os.environ["ORVA_TRACE_ID"]
# Node.js:       process.env.ORVA_TRACE_ID
# Reading them is optional — the platform records the trace for you.`,qt=`// Function A — calls B via the SDK. Trace context flows automatically.
const { invoke, jobs } = require('orva')

module.exports.handler = async (event) => {
  // F2F call — B becomes a child span under A.
  const result = await invoke('send_email', { to: event.email })

  // Job enqueue — when this job runs (now or in 6 hours), the resulting
  // execution lands in the SAME trace as A.
  await jobs.enqueue('audit_log', { action: 'sent', to: event.email })

  return { statusCode: 200, body: 'ok' }
}`,Wt=`# Send the W3C traceparent header — Orva will adopt it as the trace root.
curl -H "traceparent: 00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01" \\
     https://orva.example.com/fn/myfn/

# Response always echoes:
# X-Trace-Id: tr_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`,Xt=`{
  "error": {
    "code": "VALIDATION",
    "message": "name must be lowercase and dash-separated",
    "request_id": "req_abc123"
  }
}`,Vt=`# 1. Generate an API key in the dashboard (Keys page) or via the API
# 2. Tell the CLI where to find your Orva and which key to use
orva login \\
  --endpoint https://orva.example.com \\
  --api-key  orva_xxx_your_key_here

# Writes ~/.orva/config.yaml. Subsequent commands need no flags.
orva system health      # smoke test`,Yt=`# Init a project in cwd (creates orva.yaml + handler stub)
orva init

# Deploy from a directory. Auto-detects handler.ts when tsconfig.json
# is present; else uses the runtime default (handler.js / handler.py).
orva deploy ./my-fn \\
  --name    resize-image \\
  --runtime node24

# Override the entrypoint explicitly:
orva deploy ./my-fn --name api --runtime python314 --entrypoint app.py`,Zt=`# Invoke a function by name or fn_<id>:
orva invoke resize-image --data '{"url":"https://example.com/cat.jpg"}'

# Recent executions:
orva logs resize-image

# Single execution, with stdout/stderr:
orva logs resize-image --exec-id exec_abc123

# Live tail — SSE stream, Ctrl-C to stop:
orva logs resize-image --tail`,Jt=`# List keys (optionally by prefix)
orva kv list resize-image
orva kv list resize-image --prefix user:

# Read / write / delete
orva kv get  resize-image cache:home
orva kv put  resize-image cache:home '{"hits":42}' --ttl 3600
orva kv delete resize-image cache:home`,Qt=`# Secrets — encrypted at rest, injected as env vars at spawn:
orva secrets set    resize-image S3_BUCKET my-bucket
orva secrets list   resize-image
orva secrets delete resize-image S3_BUCKET

# Cron — fire a function on a schedule:
orva cron create --fn daily-report --expr '0 9 * * *' --tz Asia/Kolkata
orva cron list
orva cron update <cron_id> --enabled false   # pause
orva cron delete <cron_id>

# Jobs — fire-and-forget background queue:
orva jobs enqueue --fn send-email --data '{"to":"a@b.c"}'
orva jobs list --status pending
orva jobs retry  <job_id>
orva jobs delete <job_id>

# Outbound webhooks (system events):
orva webhooks create --url https://hooks.slack.com/... --events deployment.failed,job.failed
orva webhooks test   <webhook_id>

# Inbound webhook triggers (external POST → function):
orva webhooks inbound create --fn order-handler --signature stripe`,es=`orva system health        # daemon up + DB ok
orva system metrics       # JSON metrics snapshot
orva system db-stats      # on-disk breakdown (orva.db, WAL, functions/)
orva system vacuum        # rewrite SQLite to reclaim freelist pages

orva activity                          # last 50 activity rows
orva activity --tail                   # live feed (Ctrl-C)
orva activity --source mcp --limit 200 # MCP-only, last 200`,ts="<YOUR_ORVA_TOKEN>",Fn={__name:"Docs",setup(l){const I=Ds();ke.registerLanguage("python",Xs),ke.registerLanguage("javascript",Ft),ke.registerLanguage("js",Ft),ke.registerLanguage("json",Qs),ke.registerLanguage("bash",_t),ke.registerLanguage("shell",_t),ke.registerLanguage("sh",_t),ke.registerLanguage("http",eo);const f=Y(()=>window.location.origin),_=[{id:"handler",num:"01",label:"Handler"},{id:"deploy",num:"02",label:"Deploy"},{id:"config",num:"03",label:"Config"},{id:"sdk",num:"04",label:"SDK"},{id:"schedules",num:"05",label:"Schedules"},{id:"webhooks",num:"06",label:"Webhooks"},{id:"mcp",num:"07",label:"MCP"},{id:"generate",num:"08",label:"AI prompt"},{id:"tracing",num:"09",label:"Tracing"},{id:"errors",num:"10",label:"Errors"},{id:"cli",num:"11",label:"CLI"}],X=Ie("handler");let Z=null;Ls(()=>{if(typeof IntersectionObserver>"u")return;const h=new Set;Z=new IntersectionObserver(s=>{for(const T of s)T.isIntersecting?h.add(T.target.id):h.delete(T.target.id);for(const T of _)if(h.has(T.id)){X.value=T.id;break}},{rootMargin:"-20% 0px -70% 0px",threshold:0});for(const s of _){const T=document.getElementById(s.id);T&&Z.observe(T)}}),Bs(()=>{Z&&Z.disconnect()});const se=Ps(),M=Ie(!1);let z=null;const K=async()=>{await Ms()&&(M.value=!0,clearTimeout(z),z=setTimeout(()=>{M.value=!1},1500))},te=Re({setup(){return()=>a("svg",{viewBox:"0 0 256 255",width:"14",height:"14",xmlns:"http://www.w3.org/2000/svg"},[a("defs",null,[a("linearGradient",{id:"pyg1",x1:"0",y1:"0",x2:"1",y2:"1"},[a("stop",{offset:"0","stop-color":"#387EB8"}),a("stop",{offset:"1","stop-color":"#366994"})]),a("linearGradient",{id:"pyg2",x1:"0",y1:"0",x2:"1",y2:"1"},[a("stop",{offset:"0","stop-color":"#FFE052"}),a("stop",{offset:"1","stop-color":"#FFC331"})])]),a("path",{fill:"url(#pyg1)",d:"M126.9 12c-58.3 0-54.7 25.3-54.7 25.3l.1 26.2H128v8H50.5S12 67.2 12 126.1c0 58.9 33.6 56.8 33.6 56.8h19.4v-27.4s-1-33.6 33.1-33.6h55.9s32 .5 32-30.9V43.5S191.7 12 126.9 12zM95.7 29.9a10 10 0 0 1 0 20 10 10 0 0 1 0-20z"}),a("path",{fill:"url(#pyg2)",d:"M129.1 243c58.3 0 54.7-25.3 54.7-25.3l-.1-26.2H128v-8h77.5s38.5 4.4 38.5-54.5c0-58.9-33.6-56.8-33.6-56.8h-19.4v27.4s1 33.6-33.1 33.6H102s-32-.5-32 30.9v52S64.3 243 129.1 243zm30.4-17.9a10 10 0 0 1 0-20 10 10 0 0 1 0 20z"})])}}),F=Re({setup(){return()=>a("svg",{viewBox:"0 0 256 280",width:"14",height:"14",xmlns:"http://www.w3.org/2000/svg"},[a("path",{fill:"#3F873F",d:"M128 0 12 67v146l116 67 116-67V67L128 0zm0 24.6 95 54.8v121.2l-95 54.8-95-54.8V79.4l95-54.8z"}),a("path",{fill:"#3F873F",d:"M128 64c-3 0-5.7.7-8 2.3L73 92c-5 2.7-8 8-8 13.6V169c0 5.6 3 10.7 8 13.5l13 7.4c6.3 3.1 8.5 3.1 11.4 3.1 9.4 0 14.8-5.7 14.8-15.6V117c0-1-.7-1.7-1.7-1.7H103c-1 0-1.7.7-1.7 1.7v60.2c0 4.4-4.5 8.7-11.8 5.1l-13.7-7.9a1.6 1.6 0 0 1-.8-1.4v-63.4c0-.6.3-1 .8-1.4l46.8-26.9c.4-.3 1-.3 1.4 0L171 110c.5.4.8.8.8 1.4V174a1.7 1.7 0 0 1-.8 1.4l-46.8 27c-.4.2-1 .2-1.4 0l-12-7.2c-.4-.2-.8-.2-1.2 0-3.4 1.9-4 2.2-7.2 3.3-.8.3-2 .7.4 2.1l15.7 9.3c2.5 1.4 5.3 2.2 8.2 2.2 2.9 0 5.7-.8 8.2-2.2L181 184c5-2.8 8-7.9 8-13.5V107c0-5.6-3-10.7-8-13.5l-46.7-26.7a17 17 0 0 0-6.3-2.8z"})])}}),B=Re({name:"DeployPipelineDiagram",setup(){const h=[{glyph:"▣",label:"Tarball",sub:"POST /deploy"},{glyph:"⟜",label:"Extract",sub:"untar → scratch dir"},{glyph:"◍",label:"Install",sub:"npm / pip"},{glyph:"⟐",label:"Compile",sub:"tsc (TypeScript)"},{glyph:"◉",label:"Activate",sub:"rename → current"},{glyph:"✦",label:"Warm pool",sub:"pre-spawn N workers"}];return()=>a("figure",{class:"doc-diagram"},[a("figcaption",{class:"doc-diagram-cap"},"Deploy pipeline"),a("div",{class:"doc-pipeline"},h.flatMap((s,T)=>{const d=a("div",{key:`s${T}`,class:"doc-pipeline-stage"},[a("div",{class:"doc-pipeline-glyph"},s.glyph),a("div",{class:"doc-pipeline-label"},[a("span",{class:"doc-pipeline-name"},s.label),a("span",{class:"doc-pipeline-sub"},s.sub)])]),N=T<h.length-1?a("div",{key:`a${T}`,class:"doc-pipeline-arrow","aria-hidden":"true"}):null;return N?[d,N]:[d]}))])}}),de=Re({name:"TraceTreeDiagram",setup(){const s=[{fn:"api-gateway",trigger:"http",start:0,dur:220,parent:null,klass:"root"},{fn:"resize-image",trigger:"f2f",start:30,dur:90,parent:"api-gateway",klass:"child"},{fn:"send-email",trigger:"job",start:60,dur:40,parent:"api-gateway",klass:"grand"}],T=d=>d/220*100;return()=>a("figure",{class:"doc-diagram"},[a("figcaption",{class:"doc-diagram-cap"},"Causal trace — one HTTP request, three spans"),a("div",{class:"doc-trace"},[a("div",{class:"doc-trace-axis"},[a("span",null,"0 ms"),a("span",null,"220 ms")]),...s.map(d=>a("div",{key:d.fn,class:["doc-trace-row",`is-${d.klass}`]},[a("div",{class:"doc-trace-label"},[a("span",{class:"doc-trace-fn"},d.fn),a("span",{class:"doc-trace-trigger"},d.trigger)]),a("div",{class:"doc-trace-track"},[a("div",{class:"doc-trace-bar",style:{left:`${T(d.start)}%`,width:`${T(d.dur)}%`},title:`+${d.start}ms · ${d.dur}ms`})]),a("div",{class:"doc-trace-dur"},`${d.dur}ms`)])),a("div",{class:"doc-trace-legend"},[a("span",null,"Same "),a("code",{class:"doc-chip"},"trace_id"),a("span",null," across all spans · "),a("code",{class:"doc-chip"},"parent_span_id"),a("span",null," chains them into a tree.")])])])}}),J=Re({name:"WebhookDeliveryDiagram",setup(){return()=>a("figure",{class:"doc-diagram"},[a("figcaption",{class:"doc-diagram-cap"},"Signed webhook delivery"),a("div",{class:"doc-webhook"},[a("div",{class:"doc-webhook-actor"},[a("div",{class:"doc-webhook-actor-head"},"orvad"),a("div",{class:"doc-webhook-actor-body"},[a("span",null,"event fires"),a("code",{class:"doc-chip"},"deployment.succeeded")])]),a("div",{class:"doc-webhook-wire"},[a("div",{class:"doc-webhook-wire-line","aria-hidden":"true"}),a("div",{class:"doc-webhook-wire-payload"},[a("div",{class:"doc-webhook-wire-method"},"POST"),a("div",{class:"doc-webhook-wire-headers"},[a("code",null,"X-Orva-Event"),a("code",null,"X-Orva-Timestamp"),a("code",null,"X-Orva-Signature")]),a("div",{class:"doc-webhook-wire-sig"},"sha256=hex(hmac(secret, ts.body))")])]),a("div",{class:"doc-webhook-actor"},[a("div",{class:"doc-webhook-actor-head"},"your receiver"),a("div",{class:"doc-webhook-actor-body"},[a("span",null,"verify HMAC"),a("span",null,"→ 2xx within 15s or get retried")])])])])}}),$=Y(()=>[{label:"Python",lang:"python",code:`def handler(event):
    body = event.get("body") or {}
    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": {"hello": body.get("name", "world")},
    }`},{label:"Node.js",lang:"js",code:`exports.handler = async (event) => {
  const body = event.body || {};
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: { hello: body.name || 'world' },
  };
};`}]),ue=Y(()=>[{label:"curl",lang:"bash",code:`curl -X POST ${f.value}/fn/<function_id> \\
  -H 'Content-Type: application/json' \\
  -d '{"name": "Orva"}'`},{label:"fetch",lang:"js",code:`const res = await fetch('${f.value}/fn/<function_id>', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ name: 'Orva' }),
});
console.log(await res.json());`},{label:"Python",lang:"python",code:`import httpx

r = httpx.post(
    "${f.value}/fn/<function_id>",
    json={"name": "Orva"},
)
print(r.json())`}]),ne=[{id:"python314",name:"Python 3.14",entry:"handler.py",deps:"requirements.txt",icon:te},{id:"python313",name:"Python 3.13",entry:"handler.py",deps:"requirements.txt",icon:te},{id:"node24",name:"Node.js 24",entry:"handler.js",deps:"package.json",icon:F},{id:"node22",name:"Node.js 22",entry:"handler.js",deps:"package.json",icon:F}],pe=[{field:"env_vars",purpose:"Plain config",body:"Plaintext config stored on the function record. Use for feature flags and non-secret settings.",icon:Ks,iconClass:"text-violet-300"},{field:"/secrets",purpose:"Encrypted",body:"AES-256-GCM at rest. Values decrypt only into the worker environment at spawn time.",icon:Je,iconClass:"text-emerald-300"},{field:"network_mode",purpose:"Egress control",body:"none = isolated loopback. egress = outbound HTTPS allowed; firewall blocklist applies.",icon:Et,iconClass:"text-sky-300"},{field:"auth_mode",purpose:"Invoke gate",body:"none = public. platform_key = require Orva API key. signed = require HMAC.",icon:Fs,iconClass:"text-violet-300"},{field:"rate_limit_per_min",purpose:"Per-IP throttle",body:"Optional cap for public or webhook-facing functions. Exceeding it returns 429.",icon:zs,iconClass:"text-amber-300"}],_e=Y(()=>`curl -X POST ${f.value}/api/v1/functions \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"name":"hello","runtime":"python314","memory_mb":128,"cpus":0.5}'`),Se=Y(()=>`tar czf code.tar.gz handler.py requirements.txt
curl -X POST ${f.value}/api/v1/functions/<function_id>/deploy \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -F code=@code.tar.gz`),ae=Y(()=>`curl -X POST ${f.value}/api/v1/functions/<function_id>/secrets \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"key":"DATABASE_URL","value":"postgres://..."}'`),fe=Y(()=>`# generate signature
SECRET='your-shared-secret-stored-in-function-secrets'
TS=$(date +%s)
BODY='{"hello":"world"}'
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')

curl -X POST ${f.value}/fn/<function_id> \\
  -H "X-Orva-Timestamp: $TS" \\
  -H "X-Orva-Signature: sha256=$SIG" \\
  -H 'Content-Type: application/json' \\
  -d "$BODY"`),Ne=Y(()=>[{label:"curl",lang:"bash",note:"Create a daily-9am schedule for an existing function. payload is delivered as the invoke body.",code:`curl -X POST ${f.value}/api/v1/functions/<function_id>/cron \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{
    "cron_expr": "0 9 * * *",
    "enabled":   true,
    "payload":   {"task": "daily-summary"}
  }'`},{label:"Toggle / edit",lang:"bash",note:"PUT accepts any subset of {cron_expr, enabled, payload}; omitted fields keep their previous value. next_run_at is recomputed on expr changes.",code:`# pause
curl -X PUT ${f.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"enabled": false}'

# change schedule
curl -X PUT ${f.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"cron_expr": "*/15 * * * *"}'`},{label:"List & delete",lang:"bash",note:"GET /api/v1/cron lists every schedule across functions (with function_name JOIN); per-function uses the nested route.",code:`# all schedules
curl ${f.value}/api/v1/cron \\
  -H 'X-Orva-API-Key: <YOUR_KEY>'

# delete one
curl -X DELETE ${f.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>'`}]),Te=[{label:"Python",lang:"python",code:`from orva import kv

def handler(event):
    # Store with optional TTL (seconds). 0 = no expiry.
    kv.put("user:42", {"name": "Ada", "tier": "pro"}, ttl_seconds=3600)

    # Read; default returned if missing or expired.
    user = kv.get("user:42", default=None)

    # List by prefix.
    pages = kv.list(prefix="page:", limit=50)

    # Delete is idempotent.
    kv.delete("user:42")

    return {"statusCode": 200, "body": str(user)}`},{label:"Node.js",lang:"js",code:`const { kv } = require('orva')

exports.handler = async (event) => {
  await kv.put('user:42', { name: 'Ada', tier: 'pro' }, { ttlSeconds: 3600 })

  const user = await kv.get('user:42', null)

  const pages = await kv.list({ prefix: 'page:', limit: 50 })

  await kv.delete('user:42')

  return { statusCode: 200, body: JSON.stringify(user) }
}`}],Me=[{label:"Python",lang:"python",code:`from orva import invoke, OrvaError

def handler(event):
    try:
        # invoke() returns the downstream {statusCode, headers, body}.
        # body is JSON-decoded when possible.
        result = invoke("resize-image", {"url": event["body"]["url"]})
        return {"statusCode": 200, "body": result["body"]}
    except OrvaError as e:
        # 404 = function not found, 507 = call depth exceeded.
        return {"statusCode": e.status or 502, "body": str(e)}`},{label:"Node.js",lang:"js",code:`const { invoke, OrvaError } = require('orva')

exports.handler = async (event) => {
  try {
    const result = await invoke('resize-image', { url: event.body.url })
    return { statusCode: 200, body: result.body }
  } catch (e) {
    if (e instanceof OrvaError) {
      return { statusCode: e.status || 502, body: e.message }
    }
    throw e
  }
}`}],Pe=[{label:"Python",lang:"python",code:`from orva import jobs

def handler(event):
    # Fire-and-forget. Returns the job id immediately; the function
    # body runs later via the scheduler. max_attempts retries with
    # exponential backoff on 5xx / exception.
    job_id = jobs.enqueue(
        "send-welcome-email",
        {"to": event["body"]["email"]},
        max_attempts=3,
    )
    return {"statusCode": 202, "body": job_id}`},{label:"Node.js",lang:"js",code:`const { jobs } = require('orva')

exports.handler = async (event) => {
  const jobId = await jobs.enqueue(
    'send-welcome-email',
    { to: event.body.email },
    { maxAttempts: 3 }
  )
  return { statusCode: 202, body: jobId }
}`}],De=[{name:"deployment.succeeded",when:"A function build finished and the new version is active."},{name:"deployment.failed",when:"A build failed or was rejected."},{name:"function.created",when:"A new function row was created via POST /api/v1/functions."},{name:"function.updated",when:"A function config was edited via PUT /api/v1/functions/{id} (status flips during a deploy do NOT fire this — see deployment.*)."},{name:"function.deleted",when:"A function was removed."},{name:"execution.error",when:"An invocation finished with status=error or 5xx."},{name:"cron.failed",when:"A scheduled run failed (bad expr, missing fn, dispatch error, or 5xx)."},{name:"job.succeeded",when:"A queued background job finished successfully."},{name:"job.failed",when:"A queued job exhausted its retries (terminal failure)."}],ze=[{label:"Python",lang:"python",note:"Run on the receiver. Reject anything that fails verification — the signature ensures the request really came from this Orva instance.",code:`import hmac, hashlib, time

def verify(secret: str, ts: str, body: bytes, sig_header: str) -> bool:
    if abs(time.time() - int(ts)) > 300:   # 5-min skew window
        return False
    mac = hmac.new(secret.encode(), f"{ts}.".encode() + body, hashlib.sha256)
    expected = "sha256=" + mac.hexdigest()
    return hmac.compare_digest(expected, sig_header)

# In your Flask/FastAPI/etc. handler:
ts  = request.headers["X-Orva-Timestamp"]
sig = request.headers["X-Orva-Signature"]
if not verify(WEBHOOK_SECRET, ts, request.get_data(), sig):
    return "bad signature", 401`},{label:"Node.js",lang:"js",note:"Same shape as Stripe. Use timingSafeEqual to avoid sig-leak via timing.",code:`const crypto = require('crypto')

function verify(secret, ts, body, sigHeader) {
  if (Math.abs(Date.now() / 1000 - parseInt(ts, 10)) > 300) return false
  const mac = crypto.createHmac('sha256', secret)
  mac.update(ts + '.')
  mac.update(body)
  const expected = 'sha256=' + mac.digest('hex')
  if (expected.length !== sigHeader.length) return false
  return crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(sigHeader))
}

// In an express handler with raw body middleware:
app.post('/webhooks/orva', (req, res) => {
  const ok = verify(
    process.env.WEBHOOK_SECRET,
    req.headers['x-orva-timestamp'],
    req.body,                  // raw bytes — NOT parsed JSON
    req.headers['x-orva-signature']
  )
  if (!ok) return res.status(401).send('bad signature')
  res.sendStatus(200)
})`}],Ke=[{name:"http",desc:"Public HTTP request hit /fn/<id>/. Almost always a root span."},{name:"f2f",desc:"Another function called this one via orva.invoke(). Has a parent_span_id."},{name:"job",desc:"Background job runner picked up an enqueued job. Parent_span_id is whoever enqueued it."},{name:"cron",desc:"Scheduler fired a cron entry. Always a root span."},{name:"inbound",desc:"External webhook hit /webhook/{id}. Always a root span."},{name:"replay",desc:"Operator clicked Replay on a captured execution. Fresh trace, no link to original."},{name:"mcp",desc:"AI agent invoked the function via MCP invoke_function. Fresh trace."}],me=[{code:"VALIDATION",when:"Bad request body or path parameter."},{code:"UNAUTHORIZED",when:"Missing or invalid API key / session cookie."},{code:"NOT_FOUND",when:"Function, deployment, or secret doesn't exist."},{code:"RATE_LIMITED",when:"Too many requests — check the Retry-After header."},{code:"VERSION_GCD",when:"Rollback target was garbage-collected."},{code:"INSUFFICIENT_DISK",when:"Host is below min_free_disk_mb."}],Fe=[{cmd:"login",subs:"—",purpose:"Save endpoint + API key to ~/.orva/config.yaml"},{cmd:"init",subs:"—",purpose:"Scaffold an orva.yaml in the current directory"},{cmd:"deploy",subs:"[path]",purpose:"Package a directory and deploy as a function"},{cmd:"invoke",subs:"[name|id]",purpose:"POST to /fn/<id>/ and print the response"},{cmd:"logs",subs:"[name|id] [--tail]",purpose:"List recent executions; --tail follows live via SSE"},{cmd:"functions",subs:"list / get / create / delete",purpose:"CRUD for the function registry"},{cmd:"cron",subs:"list / create / update / delete",purpose:"Manage cron schedules attached to functions"},{cmd:"jobs",subs:"list / enqueue / retry / delete",purpose:"Background queue management"},{cmd:"kv",subs:"list / get / put / delete",purpose:"Browse a function’s key/value store"},{cmd:"secrets",subs:"list / set / delete",purpose:"AES-256-GCM secrets per function"},{cmd:"webhooks",subs:"list / create / test / delete / inbound",purpose:"System-event subscribers + inbound triggers"},{cmd:"routes",subs:"list / set / delete",purpose:"Custom URL → function path mappings"},{cmd:"keys",subs:"list / create / revoke",purpose:"Manage API keys"},{cmd:"activity",subs:"[--tail] [--source web|api|...]",purpose:"Paginated activity rows; live SSE with --tail"},{cmd:"system",subs:"health / metrics / db-stats / vacuum",purpose:"Server diagnostics"},{cmd:"setup",subs:"[--skip-nsjail] [--skip-rootfs]",purpose:"Install nsjail + rootfs on a bare host"},{cmd:"serve",subs:"[--port N]",purpose:"Run as the server daemon (not the CLI client)"},{cmd:"completion",subs:"bash / zsh / fish / powershell",purpose:"Emit shell completion script"}],Ge=Y(()=>{const h=(C,ut)=>ut.map(Be=>`### ${C} — ${Be.label}

${Be.note?`> ${Be.note}

`:""}\`\`\`${Be.lang}
${Be.code}
\`\`\``).join(`

`),s=`| Runtime | ID | Entrypoint | Dependencies |
|---|---|---|---|
`+ne.map(C=>`| ${C.name} | \`${C.id}\` | \`${C.entry}\` | \`${C.deps}\` |`).join(`
`),T=`| Field | Purpose | Behaviour |
|---|---|---|
`+pe.map(C=>`| \`${C.field}\` | ${C.purpose} | ${C.body} |`).join(`
`),d=`| Trigger | Meaning |
|---|---|
`+Ke.map(C=>`| \`${C.name}\` | ${C.desc} |`).join(`
`),N=`| Event | When it fires |
|---|---|
`+De.map(C=>`| \`${C.name}\` | ${C.when} |`).join(`
`),ce=`| Code | When you see it |
|---|---|
`+me.map(C=>`| \`${C.code}\` | ${C.when} |`).join(`
`),dt=`| Command | Subcommands | Purpose |
|---|---|---|
`+Fe.map(C=>`| \`orva ${C.cmd}\` | ${C.subs} | ${C.purpose} |`).join(`
`);return`# Orva — Documentation

> Everything you need to write, deploy, and operate functions on Orva.
> Generated from the in-app Docs page (\`${f.value}/web/docs\`).

## Table of contents

1. [Handler contract](#handler-contract)
2. [Deploy & invoke](#deploy--invoke)
3. [Configuration reference](#configuration-reference)
4. [SDK from inside a function](#sdk-from-inside-a-function)
5. [Schedules](#schedules)
6. [Webhooks](#webhooks)
7. [MCP — Model Context Protocol](#mcp--model-context-protocol)
8. [System prompt for AI assistants](#system-prompt-for-ai-assistants)
9. [Tracing](#tracing)
10. [Errors & recovery](#errors--recovery)
11. [CLI](#cli)

---

## Handler contract

One exported function receives the inbound HTTP event and returns an
HTTP-shaped response. The adapter handles serialization and headers.

${h("Handler",$.value)}

**Event shape:** \`method\`, \`path\`, \`headers\`, \`query\`, \`body\`.

**Response:** \`{ statusCode, headers, body }\`. Non-string bodies are
JSON-encoded by the adapter.

**Runtime env:** env vars and secrets land in \`process.env\` (Node) /
\`os.environ\` (Python).

${s}

---

## Deploy & invoke

The dashboard handles day-to-day work; these calls are for CI and
automation. Builds run async — poll \`/api/v1/deployments/<id>\` or
stream \`/api/v1/deployments/<id>/stream\` until \`phase: done\`.

### 1. Create the function row

\`\`\`bash
${_e.value}
\`\`\`

### 2. Upload code

\`\`\`bash
${Se.value}
\`\`\`

### Invoke

${h("Invoke",ue.value)}

> **Custom routes:** attach a friendly path with \`POST /api/v1/routes\`.
> Reserved prefixes: \`/api/\` \`/fn/\` \`/mcp/\` \`/web/\` \`/_orva/\`.

---

## Configuration reference

Everything below lives on the function record. Secrets are stored
encrypted and only decrypt into the worker environment at spawn time.

${T}

### Set a secret

\`\`\`bash
${ae.value}
\`\`\`

### Signed-invoke recipe (HMAC, opt-in)

\`\`\`bash
${fe.value}
\`\`\`

---

## SDK from inside a function

The bundled \`orva\` module exposes three primitives every function can
use without extra dependencies: a per-function key/value store,
in-process calls to other Orva functions, and a fire-and-forget
background job queue.

- **\`orva.kv\`** — \`put\` / \`get\` / \`delete\` / \`list\`. Per-function namespace on SQLite, optional TTL.
- **\`orva.invoke\`** — \`invoke(name, payload)\`. In-process call to another function. 8-deep call cap.
- **\`orva.jobs\`** — \`jobs.enqueue(name, payload)\`. Fire-and-forget; persisted; retried with exp backoff.

### KV — get/put with TTL

${h("KV",Te)}

> Browse / inspect / edit / delete / set keys without leaving the
> dashboard at \`/web/functions/<name>/kv\`. REST mirror at
> \`GET/PUT/DELETE /api/v1/functions/<id>/kv[/<key>]\`. MCP tools:
> \`kv_list\` / \`kv_get\` / \`kv_put\` / \`kv_delete\`.

### Function-to-function — invoke()

${h("F2F",Me)}

### Background jobs — jobs.enqueue()

${h("Jobs",Pe)}

> **Network mode:** the SDK reaches orvad over loopback through the
> host gateway, so the function needs \`network_mode: "egress"\`. On
> the default \`"none"\` the SDK throws \`OrvaUnavailableError\` with a
> clear hint.

---

## Schedules

Fire any function on a cron expression. The scheduler runs as part of
the orvad process — no external service. Manage from the Schedules
page or via the API. Standard 5-field cron with the usual shorthands
(\`@daily\`, \`@hourly\`, \`*/5 * * * *\`).

${h("Cron",Ne.value)}

> **Cron-fired headers:** every cron-triggered invocation arrives at
> the function with \`x-orva-trigger: cron\` and
> \`x-orva-cron-id: cron_…\` on the event headers, so user code can
> branch on origin.

---

## Webhooks

Operator-managed subscriptions for system events. Configure URLs from
the Webhooks page; Orva delivers signed POSTs to them when matching
events fire (deployments, function lifecycle, cron failures, job
outcomes). Subscriptions are global, not per-function.

**Headers:** \`X-Orva-Event\`, \`X-Orva-Delivery-Id\`,
\`X-Orva-Timestamp\`, \`X-Orva-Signature\`.

**Signature:** \`sha256=hex(hmac(secret, ts.body))\`. Same shape as
Stripe / signed-invoke. Receivers verify with the secret returned at
create time.

**Retries:** 5 attempts, exponential backoff (≤ 1h). Receiver must 2xx
within 15s.

${N}

### Verify a delivery

${h("Verify",ze)}

---

## MCP — Model Context Protocol

Same API surface the dashboard uses, exposed as 69 tools an agent can
call directly. API key permissions scope the available tool set.

- **Endpoint:** \`${f.value}/mcp\`
- **Auth header:** \`Authorization: Bearer <token>\`
  (fallback: \`X-Orva-API-Key: <token>\`)
- **Transport:** Streamable HTTP, MCP 2025-11-25.

> Generate a token from the Docs page in the dashboard, then drop it
> into your client config (Claude Code, Claude Desktop, Cursor, Cline,
> Codex, Windsurf, ChatGPT, etc.). Either header works against the
> same API key store with identical permission gating.

### Install snippets (primary clients)

${h("MCP",Qe.value)}

### More clients (Cursor, VS Code, Codex CLI, OpenCode, Zed, Windsurf, ChatGPT)

${h("MCP (extra)",et.value)}

### Hand-edited config files

${h("MCP config",lt.value)}

---

## System prompt for AI assistants

Paste the prompt below into ChatGPT, Claude, Gemini, Cursor, Copilot,
or any other AI tool to teach it Orva's full surface — handler
contract, runtimes, sandbox limits, the in-sandbox \`orva\` SDK
(kv / invoke / jobs), cron triggers, system-event webhooks, auth
modes, and production patterns. The model then turns "describe what I
want" into a pasteable handler on the first try.

\`\`\`text
${se}
\`\`\`

---

## Tracing

Every invocation chain is recorded as a causal trace —
**automatically, with zero changes to your function code**. HTTP
requests, F2F invokes, jobs, cron, inbound webhooks, and replays all
stitch into the same tree. The dashboard renders it as a waterfall at
\`/traces\`.

Each execution row IS a span. Spans share a \`trace_id\`; child spans
point at their parent via \`parent_span_id\`. You don't instantiate
spans, you don't import a tracer — you just write your handler and
the platform plumbs IDs through every internal hop.

### What user code sees

Two env vars are stamped per invocation. Read them only if you want to
log the trace_id alongside your own messages — they're optional.

\`\`\`text
${Gt}
\`\`\`

### Automatic propagation

When a function calls another via the SDK, the trace context flows
through automatically. The called function becomes a child span of
the caller; both share the same \`trace_id\`. Job enqueues work the
same way: \`orva.jobs.enqueue()\` records the trace context on the job
row, so when the scheduler picks the job up later, the resulting
execution lands in the same trace as the function that enqueued it
— even if the gap is hours or days.

\`\`\`js
${qt}
\`\`\`

### Triggers

Each span carries a \`trigger\` label so the UI can show how the chain
started.

${d}

### External correlation (W3C traceparent)

Send a standard \`traceparent\` header on the inbound HTTP request and
Orva makes its trace a child of yours. The same trace_id is echoed
back as \`X-Trace-Id\` on every response, so external systems can
correlate without parsing bodies.

\`\`\`bash
${Wt}
\`\`\`

### Outlier detection

Each function maintains an in-memory rolling P95 baseline over its
last 100 successful warm executions. An invocation is flagged as an
outlier when it has at least 20 baseline samples AND its duration
exceeds **P95 × 2**. Cold starts and errors are excluded from the
baseline so a flapping function can't drag it down. The flag and
baseline P95 are stored on the execution row and rendered as an amber
flag icon next to the span.

### Where to look

- \`/traces\` — list of recent traces, filterable by function / status / outlier-only.
- \`/traces/:id\` — waterfall + per-span detail. Click a span to jump to its execution in the Invocations log.
- \`GET /api/v1/traces/{id}\` — full span tree as JSON. Pair with \`list_traces\` / \`get_trace\` MCP tools for AI agents.
- \`GET /api/v1/functions/{id}/baseline\` — current P95/P99/mean for a function.

---

## Errors & recovery

Every error response uses the same envelope so log scrapers and
retries can match on \`code\`. Deploys are content-addressed; rollback
retargets the active version pointer and refreshes warm workers.

\`\`\`json
${Xt}
\`\`\`

${ce}

---

## CLI

\`orva\` is a single static binary that talks to a remote (or local)
Orva server over HTTPS. Same binary as the daemon — \`orva serve\`
starts a server, every other subcommand is a CLI client. Drop it on
operator laptops, CI runners, or anywhere bash runs.

### Install

- **Server included:** \`curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install.sh | sh\` — daemon + nsjail + rootfs + CLI.
- **CLI only:** add \`--cli-only\` for a ~10 MB binary at \`/usr/local/bin/orva\` (no service, no rootfs).
- **Inside Docker:** the dashboard image ships the CLI at the same path; \`docker exec orva orva system health\` works out of the box (auto-authed via the bootstrap key the entrypoint writes to \`~/.orva/config.yaml\`).

### Authenticate

Generate a key from the Keys page in the dashboard, then:

\`\`\`bash
${Vt}
\`\`\`

### Command index

${dt}

### Common recipes

#### Deploy

\`\`\`bash
${Yt}
\`\`\`

#### Invoke + tail logs

\`\`\`bash
${Zt}
\`\`\`

#### KV

\`\`\`bash
${Jt}
\`\`\`

#### Secrets, cron, jobs, webhooks

\`\`\`bash
${Qt}
\`\`\`

#### System health, metrics, vacuum

\`\`\`bash
${es}
\`\`\`

### Shell completion

\`\`\`bash
orva completion bash | sudo tee /etc/bash_completion.d/orva
# or zsh / fish / powershell
\`\`\`
`}),ve=Ie(!1);let j=null;const ye=async()=>{await jt(Ge.value)&&(ve.value=!0,clearTimeout(j),j=setTimeout(()=>{ve.value=!1},1500))},Q=Ie(!1),re=Ie(""),he=Ie(!1),qe=Y(()=>re.value.slice(0,12)),ee=Y(()=>re.value||ts),it=async()=>{if(!he.value){he.value=!0;try{const h=new Date().toISOString().slice(0,16).replace("T"," "),s=await Us.post("/keys",{name:"MCP — "+h,permissions:["invoke","read","write","admin"]});re.value=s.data.key}catch(h){console.error("mint mcp key failed",h),I.notify({title:"Could not mint key",message:h?.response?.data?.error?.message||h.message||"Unknown error",danger:!0})}finally{he.value=!1}}},Qe=Y(()=>[{label:"Claude Code",lang:"bash",note:"Anthropic's `claude` CLI. Restart Claude Code afterwards; `/mcp` lists Orva's 57 tools.",code:`claude mcp add --transport http --scope user orva ${f.value}/mcp --header "Authorization: Bearer ${ee.value}"`},{label:"curl",lang:"bash",note:"Talk to MCP directly. Step 1 returns a session id (Mcp-Session-Id) that Step 2 references.",code:`curl -sD - -X POST ${f.value}/mcp \\
  -H 'Authorization: Bearer ${ee.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'

curl -sX POST ${f.value}/mcp \\
  -H 'Authorization: Bearer ${ee.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H 'Mcp-Session-Id: <SID>' \\
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'`}]),et=Y(()=>[{label:"Claude Desktop",lang:"json",note:"Paste into ~/Library/Application Support/Claude/claude_desktop_config.json (macOS), %APPDATA%\\Claude\\claude_desktop_config.json (Windows), or ~/.config/Claude/claude_desktop_config.json (Linux). Restart Claude Desktop.",code:`{
  "mcpServers": {
    "orva": {
      "url": "${f.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${ee.value}"
      }
    }
  }
}`},{label:"Cursor",lang:"bash",note:"Open the link in your browser. Cursor pops an approval dialog and writes ~/.cursor/mcp.json.",code:`cursor://anysphere.cursor-deeplink/mcp/install?name=orva&config=${ct.value}`},{label:"VS Code",lang:"bash",note:'User-scoped install via the Copilot-MCP `code --add-mcp` flag. Pick "Workspace" at the prompt to write .vscode/mcp.json instead.',code:`code --add-mcp '{"name":"orva","type":"http","url":"${f.value}/mcp","headers":{"Authorization":"Bearer ${ee.value}"}}'`},{label:"Codex CLI",lang:"bash",note:"OpenAI's `codex` CLI. Writes to ~/.codex/config.toml.",code:`codex mcp add --transport streamable-http orva ${f.value}/mcp --header "Authorization: Bearer ${ee.value}"`},{label:"OpenCode",lang:"bash",note:`Interactive add. Pick "Remote", paste ${f.value}/mcp, then add the header Authorization: Bearer ${ee.value}.`,code:"opencode mcp add"},{label:"Zed",lang:"json",note:"Zed runs MCP as stdio subprocesses, so use the `mcp-remote` bridge. Paste under context_servers in ~/.config/zed/settings.json. Restart Zed.",code:`{
  "context_servers": {
    "orva": {
      "source": "custom",
      "command": "npx",
      "args": [
        "-y", "mcp-remote",
        "${f.value}/mcp",
        "--header", "Authorization:Bearer ${ee.value}"
      ]
    }
  }
}`},{label:"Windsurf",lang:"json",note:"Paste into ~/.codeium/windsurf/mcp_config.json and reload Windsurf.",code:`{
  "mcpServers": {
    "orva": {
      "serverUrl": "${f.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${ee.value}"
      }
    }
  }
}`},{label:"ChatGPT",lang:"text",note:"UI-only flow. Settings → Apps & Connectors → Developer mode → Add new connector. ChatGPT renders the tool catalog and confirms before destructive calls.",code:`URL:    ${f.value}/mcp
Auth:   API key (Bearer)
Token:  ${ee.value}`}]),ct=Y(()=>{const h=JSON.stringify({url:f.value+"/mcp",headers:{Authorization:"Bearer "+ee.value}});return typeof window.btoa=="function"?window.btoa(h):h}),lt=Y(()=>[{label:"Cursor (global)",lang:"json",note:"Paste into ~/.cursor/mcp.json, or .cursor/mcp.json in your project root for a per-workspace install.",code:`{
  "mcpServers": {
    "orva": {
      "url": "${f.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${ee.value}"
      }
    }
  }
}`},{label:"Cline",lang:"json",note:"In VS Code: open Cline → MCP icon → Configure MCP Servers. Cline writes cline_mcp_settings.json.",code:`{
  "mcpServers": {
    "orva": {
      "url": "${f.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${ee.value}"
      },
      "disabled": false
    }
  }
}`}]),P=Re({name:"CodeBlock",props:{code:{type:String,required:!0},lang:{type:String,default:""}},setup(h){const s=Ie(!1),T=async()=>{await jt(h.code)&&(s.value=!0,setTimeout(()=>{s.value=!1},1200))},d=Y(()=>{const N=(h.lang||"").toLowerCase();if(N&&ke.getLanguage(N))try{return ke.highlight(h.code,{language:N,ignoreIllegals:!0}).value}catch{}return h.code.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;")});return()=>a("div",{class:"codeblock"},[a("div",{class:"codeblock-bar"},[a("span",{class:"codeblock-lang"},h.lang||""),a("button",{class:"codeblock-copy",onClick:T,title:"Copy code"},[s.value?a(yt,{class:"w-3 h-3"}):a(wt,{class:"w-3 h-3"}),s.value?"Copied":"Copy"])]),a("pre",{class:"codeblock-pre"},[a("code",{class:`hljs language-${(h.lang||"text").toLowerCase()}`,innerHTML:d.value})])])}}),ie=Re({name:"TabbedCode",props:{tabs:{type:Array,required:!0},storageKey:{type:String,default:""}},setup(h){const s=(()=>{try{if(h.storageKey){const N=localStorage.getItem(h.storageKey);if(N&&h.tabs.some(ce=>ce.label===N))return N}}catch{}return h.tabs[0]?.label})(),T=Ie(s),d=N=>{T.value=N;try{h.storageKey&&localStorage.setItem(h.storageKey,N)}catch{}};return()=>{const N=h.tabs.find(ce=>ce.label===T.value)||h.tabs[0];return a("div",{class:"tabbed"},[a("div",{class:"tabbed-tabs"},h.tabs.map(ce=>a("button",{key:ce.label,class:["tabbed-tab",{active:ce.label===T.value}],onClick:()=>d(ce.label)},ce.label))),N.note?a("div",{class:"tabbed-note"},N.note):null,a(P,{code:N.code,lang:N.lang})])}}}),Le=Re({name:"Callout",props:{title:{type:String,default:""},icon:{type:[Object,Function],default:null}},setup(h,{slots:s}){return()=>a("div",{class:"callout"},[a("div",{class:"callout-head"},[h.icon?a(h.icon,{class:"callout-icon"}):null,h.title?a("span",null,h.title):null]),a("div",{class:"callout-body"},s.default?.())])}});return(h,s)=>{const T=Hs("router-link");return W(),be("div",to,[t("header",so,[s[3]||(s[3]=t("div",{class:"docs-hero-bg","aria-hidden":"true"},null,-1)),t("div",oo,[t("div",no,[s[1]||(s[1]=t("div",{class:"docs-hero-text"},[t("h1",{class:"docs-hero-title"}," Documentation "),t("p",{class:"docs-hero-sub"}," Everything you need to write, deploy, and operate functions on Orva. Handler contract, deploy + invoke, SDK, MCP, tracing, error taxonomy. ")],-1)),t("div",ao,[t("button",{class:Ve(["docs-hero-copy-icon",{copied:ve.value}]),title:ve.value?"Copied":"Copy entire docs page as Markdown","aria-label":ve.value?"Markdown copied to clipboard":"Copy entire docs page as Markdown",onClick:ye},[ve.value?(W(),Ye(m(yt),{key:0,class:"w-4 h-4"})):(W(),Ye(m(wt),{key:1,class:"w-4 h-4"}))],10,ro)])]),t("nav",io,[s[2]||(s[2]=t("span",{class:"docs-hero-toc-label"},"Jump to",-1)),(W(),be(He,null,Ue(_,d=>t("a",{key:d.id,href:`#${d.id}`,class:Ve(["docs-hero-toc-link",{active:X.value===d.id}])},[t("span",lo,R(d.num),1),t("span",null,R(d.label),1)],10,co)),64))])])]),t("section",uo,[s[5]||(s[5]=t("div",{class:"doc-section-head"},[t("span",{class:"doc-section-num"},"01"),t("div",null,[t("h2",{class:"doc-section-title"}," Handler contract "),t("p",{class:"doc-lede"}," One exported function receives the inbound HTTP event and returns an HTTP-shaped response. The adapter handles serialization and headers. ")])],-1)),E(m(ie),{tabs:$.value,"storage-key":"docs.handler"},null,8,["tabs"]),s[6]||(s[6]=le('<div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Event shape </div><div class="doc-card-body"><code class="doc-chip">method</code><code class="doc-chip">path</code><code class="doc-chip">headers</code><code class="doc-chip">query</code><code class="doc-chip">body</code></div></div><div class="doc-card"><div class="doc-microlabel"> Response </div><div class="doc-card-body"><code class="doc-chip">{ statusCode, headers, body }</code><p class="mt-1.5 text-foreground-muted"> Non-string bodies are JSON-encoded by the adapter. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Runtime env </div><div class="doc-card-body"> Env vars and secrets land in <code class="doc-chip">process.env</code> / <code class="doc-chip">os.environ</code>. </div></div></div>',1)),t("div",po,[t("table",ho,[s[4]||(s[4]=t("thead",null,[t("tr",null,[t("th",null,"Runtime"),t("th",null,"ID"),t("th",{class:"hidden sm:table-cell"}," Entrypoint "),t("th",{class:"hidden md:table-cell"}," Dependencies ")])],-1)),t("tbody",null,[(W(),be(He,null,Ue(ne,d=>t("tr",{key:d.id},[t("td",go,[(W(),Ye(Ht(d.icon),{class:"shrink-0"})),b(" "+R(d.name),1)]),t("td",bo,R(d.id),1),t("td",fo,R(d.entry),1),t("td",mo,R(d.deps),1)])),64))])])])]),t("section",vo,[s[11]||(s[11]=le('<div class="doc-section-head"><span class="doc-section-num">02</span><div><h2 class="doc-section-title"> Deploy &amp; invoke </h2><p class="doc-lede"> The dashboard handles day-to-day work; these calls are for CI and automation. Builds run async — poll <code class="doc-chip">/api/v1/deployments/&lt;id&gt;</code> or stream <code class="doc-chip">/api/v1/deployments/&lt;id&gt;/stream</code> until <code class="doc-chip">phase: done</code>. </p></div></div>',1)),E(m(B)),t("div",yo,[t("div",wo,[s[7]||(s[7]=t("div",{class:"doc-step-label"},[t("span",{class:"doc-step-num"},"1"),b(" Create the function row ")],-1)),E(m(P),{code:_e.value,lang:"bash"},null,8,["code"])]),t("div",Eo,[s[8]||(s[8]=t("div",{class:"doc-step-label"},[t("span",{class:"doc-step-num"},"2"),b(" Upload code ")],-1)),E(m(P),{code:Se.value,lang:"bash"},null,8,["code"])])]),t("div",ko,[s[9]||(s[9]=t("div",{class:"doc-microlabel"}," Invoke ",-1)),E(m(ie),{tabs:ue.value,"storage-key":"docs.invoke"},null,8,["tabs"])]),E(m(Le),{icon:m(Et),title:"Custom routes"},{default:Oe(()=>[...s[10]||(s[10]=[b(" Attach a friendly path with ",-1),t("code",{class:"doc-chip"},"POST /api/v1/routes",-1),b(". Reserved prefixes: ",-1),t("code",{class:"doc-chip"},"/api/",-1),t("code",{class:"doc-chip"},"/fn/",-1),t("code",{class:"doc-chip"},"/mcp/",-1),t("code",{class:"doc-chip"},"/web/",-1),t("code",{class:"doc-chip"},"/_orva/",-1),b(". ",-1)])]),_:1},8,["icon"])]),t("section",_o,[s[15]||(s[15]=t("div",{class:"doc-section-head"},[t("span",{class:"doc-section-num"},"03"),t("div",null,[t("h2",{class:"doc-section-title"}," Configuration reference "),t("p",{class:"doc-lede"}," Everything below lives on the function record. Secrets are stored encrypted and only decrypt into the worker environment at spawn time. ")])],-1)),t("div",So,[t("table",To,[s[12]||(s[12]=t("thead",null,[t("tr",null,[t("th",null,"Field"),t("th",{class:"hidden sm:table-cell"}," Purpose "),t("th",null,"Behaviour")])],-1)),t("tbody",null,[(W(),be(He,null,Ue(pe,d=>t("tr",{key:d.field,class:"align-top"},[t("td",xo,[(W(),Ye(Ht(d.icon),{class:Ve(["w-3.5 h-3.5 shrink-0",d.iconClass])},null,8,["class"])),t("code",null,R(d.field),1)]),t("td",Co,R(d.purpose),1),t("td",Ao,R(d.body),1)])),64))])])]),t("div",Oo,[s[13]||(s[13]=t("div",{class:"doc-microlabel"}," Set a secret ",-1)),E(m(P),{code:ae.value,lang:"bash"},null,8,["code"])]),t("details",Io,[t("summary",Ro,[E(m(Ut),{class:"w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted"}),s[14]||(s[14]=b(" Signed-invoke recipe (HMAC, opt-in) ",-1))]),t("div",No,[E(m(P),{code:fe.value,lang:"bash"},null,8,["code"])])])]),t("section",Mo,[s[21]||(s[21]=le('<div class="doc-section-head"><span class="doc-section-num">04</span><div><h2 class="doc-section-title"> SDK from inside a function </h2><p class="doc-lede"> The bundled <code class="doc-chip">orva</code> module exposes three primitives every function can use without extra dependencies: a per-function key/value store, in-process calls to other Orva functions, and a fire-and-forget background job queue. Routes through the per-process internal token injected at worker spawn time. </p></div></div><div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.kv</code></div><div class="doc-card-body"><code class="doc-chip">put / get / delete / list</code><p class="mt-1.5 text-foreground-muted"> Per-function namespace on SQLite, optional TTL. </p></div></div><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.invoke</code></div><div class="doc-card-body"><code class="doc-chip">invoke(name, payload)</code><p class="mt-1.5 text-foreground-muted"> In-process call to another function. 8-deep call cap. </p></div></div><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.jobs</code></div><div class="doc-card-body"><code class="doc-chip">jobs.enqueue(name, payload)</code><p class="mt-1.5 text-foreground-muted"> Fire-and-forget; persisted; retried with exp backoff. </p></div></div></div>',2)),t("div",Po,[s[16]||(s[16]=t("div",{class:"doc-microlabel"}," KV — get/put with TTL ",-1)),E(m(ie),{tabs:Te,"storage-key":"docs.sdk.kv"}),s[17]||(s[17]=le('<p class="text-xs text-foreground-muted"> Browse / inspect / edit / delete / set keys without leaving the dashboard at <code class="doc-chip">/web/functions/&lt;name&gt;/kv</code> (or click the <code class="doc-chip">KV</code> button in the editor&#39;s action bar). REST mirror at <code class="doc-chip">GET/PUT/DELETE /api/v1/functions/&lt;id&gt;/kv[/&lt;key&gt;]</code>; MCP tools <code class="doc-chip">kv_list</code> / <code class="doc-chip">kv_get</code> / <code class="doc-chip">kv_put</code> / <code class="doc-chip">kv_delete</code> for agents. </p>',1))]),t("div",Do,[s[18]||(s[18]=t("div",{class:"doc-microlabel"}," Function-to-function — invoke() ",-1)),E(m(ie),{tabs:Me,"storage-key":"docs.sdk.invoke"})]),t("div",Lo,[s[19]||(s[19]=t("div",{class:"doc-microlabel"}," Background jobs — jobs.enqueue() ",-1)),E(m(ie),{tabs:Pe,"storage-key":"docs.sdk.jobs"})]),E(m(Le),{icon:m(Et),title:"Network mode"},{default:Oe(()=>[...s[20]||(s[20]=[b(" The SDK reaches orvad over loopback through the host gateway, so the function needs ",-1),t("code",{class:"doc-chip"},'network_mode: "egress"',-1),b(". On the default ",-1),t("code",{class:"doc-chip"},'"none"',-1),b(" the SDK throws ",-1),t("code",{class:"doc-chip"},"OrvaUnavailableError",-1),b(" with a clear hint. ",-1)])]),_:1},8,["icon"])]),t("section",Bo,[t("div",$o,[s[32]||(s[32]=t("span",{class:"doc-section-num"},"05",-1)),t("div",null,[s[31]||(s[31]=t("h2",{class:"doc-section-title"}," Schedules ",-1)),t("p",jo,[s[23]||(s[23]=b(" Fire any function on a cron expression. The scheduler runs as part of the orvad process — no external service. Manage from the ",-1)),E(T,{to:"/cron",class:"text-foreground hover:text-white underline decoration-dotted underline-offset-4"},{default:Oe(()=>[...s[22]||(s[22]=[b("Schedules page",-1)])]),_:1}),s[24]||(s[24]=b(" or via the API. Standard 5-field cron with the usual shorthands (",-1)),s[25]||(s[25]=t("code",{class:"doc-chip"},"@daily",-1)),s[26]||(s[26]=b(", ",-1)),s[27]||(s[27]=t("code",{class:"doc-chip"},"@hourly",-1)),s[28]||(s[28]=b(", ",-1)),s[29]||(s[29]=t("code",{class:"doc-chip"},"*/5 * * * *",-1)),s[30]||(s[30]=b("). ",-1))])])]),E(m(ie),{tabs:Ne.value,"storage-key":"docs.cron"},null,8,["tabs"]),E(m(Le),{icon:m($s),title:"Cron-fired headers"},{default:Oe(()=>[...s[33]||(s[33]=[b(" Every cron-triggered invocation arrives at the function with ",-1),t("code",{class:"doc-chip"},"x-orva-trigger: cron",-1),b(" and ",-1),t("code",{class:"doc-chip"},"x-orva-cron-id: cron_…",-1),b(" on the event headers, so user code can branch on origin. ",-1)])]),_:1},8,["icon"])]),t("section",Ho,[t("div",Uo,[s[38]||(s[38]=t("span",{class:"doc-section-num"},"06",-1)),t("div",null,[s[37]||(s[37]=t("h2",{class:"doc-section-title"}," Webhooks ",-1)),t("p",zo,[s[35]||(s[35]=b(" Operator-managed subscriptions for system events. Configure URLs from the ",-1)),E(T,{to:"/webhooks",class:"text-foreground hover:text-white underline decoration-dotted underline-offset-4"},{default:Oe(()=>[...s[34]||(s[34]=[b("Webhooks page",-1)])]),_:1}),s[36]||(s[36]=b("; Orva delivers signed POSTs to them when matching events fire (deployments, function lifecycle, cron failures, job outcomes). Subscriptions are global, not per-function. ",-1))])])]),E(m(J)),s[41]||(s[41]=le('<div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Headers </div><div class="doc-card-body"><code class="doc-chip">X-Orva-Event</code><code class="doc-chip">X-Orva-Delivery-Id</code><code class="doc-chip">X-Orva-Timestamp</code><code class="doc-chip">X-Orva-Signature</code></div></div><div class="doc-card"><div class="doc-microlabel"> Signature </div><div class="doc-card-body"><code class="doc-chip">sha256=hex(hmac(secret, ts.body))</code><p class="mt-1.5 text-foreground-muted"> Same shape as Stripe / signed-invoke. Receivers verify with the secret returned at create time. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Retries </div><div class="doc-card-body"><code class="doc-chip">5 attempts</code><code class="doc-chip">exp backoff (≤ 1h)</code><p class="mt-1.5 text-foreground-muted"> Receiver must 2xx within 15s. </p></div></div></div>',1)),t("div",Ko,[t("table",Fo,[s[39]||(s[39]=t("thead",null,[t("tr",null,[t("th",null,"Event"),t("th",null,"When it fires")])],-1)),t("tbody",null,[(W(),be(He,null,Ue(De,d=>t("tr",{key:d.name},[t("td",Go,[t("code",null,R(d.name),1)]),t("td",qo,R(d.when),1)])),64))])])]),t("div",Wo,[s[40]||(s[40]=t("div",{class:"doc-microlabel"}," Verify a delivery ",-1)),E(m(ie),{tabs:ze,"storage-key":"docs.webhooks.verify"})])]),t("section",Xo,[s[51]||(s[51]=t("div",{class:"doc-section-head"},[t("span",{class:"doc-section-num"},"07"),t("div",null,[t("h2",{class:"doc-section-title"}," MCP — Model Context Protocol "),t("p",{class:"doc-lede"}," Same API surface the dashboard uses, exposed as 57 tools an agent can call directly. API key permissions scope the available tool set. ")])],-1)),t("div",Vo,[t("div",Yo,[s[42]||(s[42]=t("div",{class:"doc-microlabel"}," Endpoint ",-1)),t("div",Zo,[t("code",Jo,R(f.value)+"/mcp",1)])]),s[43]||(s[43]=le('<div class="doc-card"><div class="doc-microlabel"> Auth header </div><div class="doc-card-body"><code class="doc-chip break-all">Authorization: Bearer &lt;token&gt;</code><p class="mt-1.5 text-foreground-muted"> Or as a fallback: <code class="doc-chip">X-Orva-API-Key: &lt;token&gt;</code></p></div></div><div class="doc-card"><div class="doc-microlabel"> Transport </div><div class="doc-card-body"><code class="doc-chip">Streamable HTTP</code><code class="doc-chip">MCP 2025-11-25</code></div></div>',2))]),E(m(Le),{icon:m(Je),title:"Two header formats; same auth"},{default:Oe(()=>[...s[44]||(s[44]=[b(" Either header works against the same API key store with identical permission gating. ",-1),t("code",{class:"doc-chip"},"Authorization: Bearer",-1),b(" is the MCP / OAuth 2 spec form — every MCP SDK (Claude Code, Claude Desktop, Cursor, mcp-remote, Python ",-1),t("code",{class:"doc-chip"},"mcp",-1),b(") configures it natively, so prefer it for new setups. ",-1),t("code",{class:"doc-chip"},"X-Orva-API-Key",-1),b(" is the same header the REST API accepts — useful when a tool reuses an existing Orva REST integration. Internally both paths SHA-256-hash the token and look it up against the same ",-1),t("code",{class:"doc-chip"},"api_keys",-1),b(" table. ",-1)])]),_:1},8,["icon"]),t("div",Qo,[t("div",en,[E(m(Je),{class:"w-4 h-4 shrink-0 text-foreground-muted"}),re.value?(W(),be("span",sn,[s[47]||(s[47]=b(" Token minted: ",-1)),t("code",on,R(qe.value)+"…",1),s[48]||(s[48]=b(" — shown once, copy now. ",-1))])):(W(),be("span",tn,[s[45]||(s[45]=b(" Snippets show ",-1)),t("code",{class:"doc-chip"},R(ts)),s[46]||(s[46]=b(". Mint a token to substitute it everywhere. ",-1))]))]),t("button",{class:"doc-token-btn",disabled:he.value,onClick:it},[E(m(Je),{class:"w-3.5 h-3.5"}),b(" "+R(re.value?"Mint another":he.value?"Minting…":"Generate token"),1)],8,nn)]),E(m(ie),{tabs:Qe.value,"storage-key":"docs.mcp.install"},null,8,["tabs"]),t("details",an,[t("summary",rn,[E(m(Ut),{class:"w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted"}),s[49]||(s[49]=b(" More clients (Cursor, VS Code, Codex CLI, OpenCode, Zed, Windsurf, ChatGPT, manual config) ",-1))]),t("div",cn,[E(m(ie),{tabs:et.value,"storage-key":"docs.mcp.install.more"},null,8,["tabs"]),s[50]||(s[50]=t("div",{class:"doc-microlabel pt-1"}," Hand-edited config files ",-1)),E(m(ie),{tabs:lt.value,"storage-key":"docs.mcp.manual"},null,8,["tabs"])])])]),t("section",ln,[s[52]||(s[52]=le('<div class="doc-section-head"><span class="doc-section-num">08</span><div><h2 class="doc-section-title"> System prompt for AI assistants </h2><p class="doc-lede"> Paste the prompt below into ChatGPT, Claude, Gemini, Cursor, Copilot, or any other AI tool to teach it Orva&#39;s full surface — handler contract, runtimes, sandbox limits, the in-sandbox <code class="doc-chip">orva</code> SDK (kv / invoke / jobs), cron triggers, system-event webhooks, auth modes, and production patterns. The model then turns &quot;describe what I want&quot; into a pasteable handler on the first try. </p></div></div>',1)),t("div",dn,[t("button",{class:Ve(["ai-copy-btn",{copied:M.value}]),onClick:K},[M.value?(W(),Ye(m(yt),{key:0,class:"w-3.5 h-3.5"})):(W(),Ye(m(wt),{key:1,class:"w-3.5 h-3.5"})),b(" "+R(M.value?"Copied":"Copy system prompt"),1)],2)]),t("div",{class:Ve(["prompt-collapse",{expanded:Q.value}])},[E(m(P),{code:m(se),lang:"text"},null,8,["code"]),Q.value?js("",!0):(W(),be("div",un))],2),t("button",{class:"prompt-expand-btn","aria-expanded":Q.value,onClick:s[0]||(s[0]=d=>Q.value=!Q.value)},[E(m(Ns),{class:Ve(["w-3.5 h-3.5 transition-transform",{"rotate-180":Q.value}])},null,8,["class"]),b(" "+R(Q.value?"Collapse system prompt":"Expand full system prompt (~400 lines)"),1)],8,pn)]),t("section",hn,[s[54]||(s[54]=le('<div class="doc-section-head"><span class="doc-section-num">09</span><div><h2 class="doc-section-title"> Tracing </h2><p class="doc-lede"> Every invocation chain is recorded as a causal trace — automatically, with <strong>zero changes to your function code</strong>. HTTP requests, F2F invokes, jobs, cron, inbound webhooks, and replays all stitch into the same tree. The dashboard renders it as a waterfall at <code class="doc-chip">/traces</code>. </p></div></div><p class="doc-prose"> Each execution row IS a span. Spans share a <code class="doc-chip">trace_id</code>; child spans point at their parent via <code class="doc-chip">parent_span_id</code>. You don&#39;t instantiate spans, you don&#39;t import a tracer — you just write your handler and the platform plumbs IDs through every internal hop. </p>',2)),E(m(de)),s[55]||(s[55]=t("h3",{class:"doc-h3"},"What user code sees",-1)),s[56]||(s[56]=t("p",{class:"doc-prose"}," Two env vars are stamped per invocation. Read them only if you want to log the trace_id alongside your own messages — they're optional. ",-1)),E(m(P),{code:Gt,lang:"text"}),s[57]||(s[57]=t("h3",{class:"doc-h3"},"Automatic propagation",-1)),s[58]||(s[58]=t("p",{class:"doc-prose"},[b(" When a function calls another via the SDK, the trace context flows through automatically. The called function becomes a child span of the caller; both share the same "),t("code",{class:"doc-chip"},"trace_id"),b(". ")],-1)),E(m(P),{code:qt,lang:"js"}),s[59]||(s[59]=le('<p class="doc-prose"> Job enqueues work the same way: <code class="doc-chip">orva.jobs.enqueue()</code> records the trace context on the job row. When the scheduler picks the job up later, the resulting execution lands in the same trace as the function that enqueued it — even if the gap is hours or days. </p><h3 class="doc-h3">Triggers</h3><p class="doc-prose"> Each span carries a <code class="doc-chip">trigger</code> label so the UI can show how the chain started. </p>',3)),t("div",gn,[t("table",bn,[s[53]||(s[53]=t("thead",null,[t("tr",null,[t("th",null,"Trigger"),t("th",null,"Meaning")])],-1)),t("tbody",null,[(W(),be(He,null,Ue(Ke,d=>t("tr",{key:d.name},[t("td",fn,[t("code",null,R(d.name),1)]),t("td",mn,R(d.desc),1)])),64))])])]),s[60]||(s[60]=t("h3",{class:"doc-h3"},"External correlation (W3C traceparent)",-1)),s[61]||(s[61]=t("p",{class:"doc-prose"},[b(" Send a standard "),t("code",{class:"doc-chip"},"traceparent"),b(" header on the inbound HTTP request and Orva makes its trace a child of yours. The same trace_id is echoed back as "),t("code",{class:"doc-chip"},"X-Trace-Id"),b(" on every response, so external systems can correlate without parsing bodies. ")],-1)),E(m(P),{code:Wt,lang:"bash"}),s[62]||(s[62]=le('<h3 class="doc-h3">Outlier detection</h3><p class="doc-prose"> Each function maintains an in-memory rolling P95 baseline over its last 100 successful warm executions. An invocation is flagged as an outlier when it has at least 20 baseline samples AND its duration exceeds <strong>P95 × 2</strong>. Cold starts and errors are excluded from the baseline so a flapping function can&#39;t drag it down. The flag and baseline P95 are stored on the execution row and rendered as an amber flag icon next to the span. </p><h3 class="doc-h3">Where to look</h3><ul class="doc-list"><li><code class="doc-chip">/traces</code> — list of recent traces, filterable by function / status / outlier-only.</li><li><code class="doc-chip">/traces/:id</code> — waterfall + per-span detail. Click a span to jump to its execution in the Invocations log.</li><li><code class="doc-chip">GET /api/v1/traces/{id}</code> — full span tree as JSON. Pair with <code class="doc-chip">list_traces</code> / <code class="doc-chip">get_trace</code> MCP tools for AI agents.</li><li><code class="doc-chip">GET /api/v1/functions/{id}/baseline</code> — current P95/P99/mean for a function.</li></ul>',4))]),t("section",vn,[s[64]||(s[64]=le('<div class="doc-section-head"><span class="doc-section-num">10</span><div><h2 class="doc-section-title"> Errors &amp; recovery </h2><p class="doc-lede"> Every error response uses the same envelope so log scrapers and retries can match on <code class="doc-chip">code</code>. Deploys are content-addressed; rollback retargets the active version pointer and refreshes warm workers. </p></div></div>',1)),E(m(P),{code:Xt,lang:"json"}),t("div",yn,[t("table",wn,[s[63]||(s[63]=t("thead",null,[t("tr",null,[t("th",null,"Code"),t("th",null,"When you see it")])],-1)),t("tbody",null,[(W(),be(He,null,Ue(me,d=>t("tr",{key:d.code},[t("td",En,[t("code",null,R(d.code),1)]),t("td",kn,R(d.when),1)])),64))])])])]),t("section",_n,[s[81]||(s[81]=le('<div class="doc-section-head"><span class="doc-section-num">11</span><div><h2 class="doc-section-title"> CLI </h2><p class="doc-lede"><code class="doc-chip">orva</code> is a single static binary that talks to a remote (or local) Orva server over HTTPS. Same binary as the daemon — <code class="doc-chip">orva serve</code> starts a server, every other subcommand is a CLI client. Drop it on operator laptops, CI runners, or anywhere bash runs. </p></div></div><div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel">Install (server included)</div><div class="doc-card-body"><code class="doc-chip">curl … install.sh | sh</code><p class="mt-1.5 text-foreground-muted"> Full install: daemon + nsjail + rootfs + CLI. </p></div></div><div class="doc-card"><div class="doc-microlabel">Install (CLI only)</div><div class="doc-card-body"><code class="doc-chip">install.sh --cli-only</code><p class="mt-1.5 text-foreground-muted"> ~10 MB binary at <code>/usr/local/bin/orva</code>. No service. </p></div></div><div class="doc-card"><div class="doc-microlabel">Inside Docker</div><div class="doc-card-body"><code class="doc-chip">docker exec orva orva …</code><p class="mt-1.5 text-foreground-muted"> CLI auto-authed via <code>~/.orva/config.yaml</code>. </p></div></div></div><h3 class="doc-h3">Authenticate</h3>',3)),t("p",Sn,[s[66]||(s[66]=b(" The CLI reads ",-1)),s[67]||(s[67]=t("code",{class:"doc-chip"},"~/.orva/config.yaml",-1)),s[68]||(s[68]=b(" for ",-1)),s[69]||(s[69]=t("code",{class:"doc-chip"},"endpoint",-1)),s[70]||(s[70]=b(" + ",-1)),s[71]||(s[71]=t("code",{class:"doc-chip"},"api_key",-1)),s[72]||(s[72]=b(". Generate a key from ",-1)),E(T,{to:"/api-keys",class:"text-foreground hover:text-white underline decoration-dotted underline-offset-4"},{default:Oe(()=>[...s[65]||(s[65]=[b("Keys",-1)])]),_:1}),s[73]||(s[73]=b(" in the dashboard, then: ",-1))]),E(m(P),{code:Vt,lang:"bash"}),s[82]||(s[82]=t("h3",{class:"doc-h3"},"Command index",-1)),t("div",Tn,[t("table",xn,[s[74]||(s[74]=t("thead",null,[t("tr",null,[t("th",null,"Command"),t("th",null,"Subcommands"),t("th",{class:"hidden md:table-cell"},"Purpose")])],-1)),t("tbody",null,[(W(),be(He,null,Ue(Fe,d=>t("tr",{key:d.cmd},[t("td",Cn,[t("code",null,"orva "+R(d.cmd),1)]),t("td",An,R(d.subs),1),t("td",On,R(d.purpose),1)])),64))])])]),s[83]||(s[83]=t("h3",{class:"doc-h3"},"Common recipes",-1)),t("div",In,[s[75]||(s[75]=t("div",{class:"doc-microlabel"},"Deploy a function from a directory",-1)),E(m(P),{code:Yt,lang:"bash"})]),t("div",Rn,[s[76]||(s[76]=t("div",{class:"doc-microlabel"},"Invoke + tail logs",-1)),E(m(P),{code:Zt,lang:"bash"})]),t("div",Nn,[s[77]||(s[77]=t("div",{class:"doc-microlabel"},"Manage KV state",-1)),E(m(P),{code:Jt,lang:"bash"})]),t("div",Mn,[s[78]||(s[78]=t("div",{class:"doc-microlabel"},"Secrets, cron, jobs, webhooks",-1)),E(m(P),{code:Qt,lang:"bash"})]),t("div",Pn,[s[79]||(s[79]=t("div",{class:"doc-microlabel"},"System health, metrics, vacuum",-1)),E(m(P),{code:es,lang:"bash"})]),E(m(Le),{icon:m(Je),title:"Shell completion"},{default:Oe(()=>[...s[80]||(s[80]=[b(" Generate completion for your shell: ",-1),t("code",{class:"doc-chip"},"orva completion bash | sudo tee /etc/bash_completion.d/orva",-1),b(", or ",-1),t("code",{class:"doc-chip"},"zsh",-1),b(" / ",-1),t("code",{class:"doc-chip"},"fish",-1),b(" / ",-1),t("code",{class:"doc-chip"},"powershell",-1),b(". Tab-completes commands, subcommands, and flags. ",-1)])]),_:1},8,["icon"])])])}}};export{Fn as default};
