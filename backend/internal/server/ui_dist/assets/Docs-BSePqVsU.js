import{C as lt,c as Et}from"./clipboard-zR9k88eM.js";import{C as Ns,a as Ms,b as Ps}from"./aiPrompts-C4MB-u44.js";import{c as Ds,z as Ls,o as Bs,L as $s,a as ge,b as t,q as qe,m as Ne,f as m,k as b,t as I,F as We,n as Xe,d as k,ae as le,h as Me,aQ as js,g as Hs,r as Ae,p as Y,ap as Pe,y as a,Q as Us,I as zs,j as U,X as Ht,H as Ks}from"./index-BuCk8NFr.js";import{C as _t}from"./copy-BJPeCake.js";import{G as St}from"./globe-_HRxSDRA.js";import{C as Ut}from"./chevron-right-vCOT2swJ.js";import{K as tt}from"./key-round-BgVGhQXx.js";import{V as Fs}from"./variable-B78a5XvB.js";import{L as Gs}from"./lock-Bom4kLJ2.js";const qs=Ds("message-square",[["path",{d:"M22 17a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 21.286V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2z",key:"18887p"}]]);function Ws(l){return l&&l.__esModule&&Object.prototype.hasOwnProperty.call(l,"default")?l.default:l}var Tt,zt;function Xs(){if(zt)return Tt;zt=1;function l(e){return e instanceof Map?e.clear=e.delete=e.set=function(){throw new Error("map is read-only")}:e instanceof Set&&(e.add=e.clear=e.delete=function(){throw new Error("set is read-only")}),Object.freeze(e),Object.getOwnPropertyNames(e).forEach(o=>{const r=e[o],y=typeof r;(y==="object"||y==="function")&&!Object.isFrozen(r)&&l(r)}),e}class R{constructor(o){o.data===void 0&&(o.data={}),this.data=o.data,this.isMatchIgnored=!1}ignoreMatch(){this.isMatchIgnored=!0}}function f(e){return e.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;").replace(/'/g,"&#x27;")}function _(e,...o){const r=Object.create(null);for(const y in e)r[y]=e[y];return o.forEach(function(y){for(const P in y)r[P]=y[P]}),r}const X="</span>",Z=e=>!!e.scope,se=(e,{prefix:o})=>{if(e.startsWith("language:"))return e.replace("language:","language-");if(e.includes(".")){const r=e.split(".");return[`${o}${r.shift()}`,...r.map((y,P)=>`${y}${"_".repeat(P+1)}`)].join(" ")}return`${o}${e}`};class M{constructor(o,r){this.buffer="",this.classPrefix=r.classPrefix,o.walk(this)}addText(o){this.buffer+=f(o)}openNode(o){if(!Z(o))return;const r=se(o.scope,{prefix:this.classPrefix});this.span(r)}closeNode(o){Z(o)&&(this.buffer+=X)}value(){return this.buffer}span(o){this.buffer+=`<span class="${o}">`}}const z=(e={})=>{const o={children:[]};return Object.assign(o,e),o};class K{constructor(){this.rootNode=z(),this.stack=[this.rootNode]}get top(){return this.stack[this.stack.length-1]}get root(){return this.rootNode}add(o){this.top.children.push(o)}openNode(o){const r=z({scope:o});this.add(r),this.stack.push(r)}closeNode(){if(this.stack.length>1)return this.stack.pop()}closeAllNodes(){for(;this.closeNode(););}toJSON(){return JSON.stringify(this.rootNode,null,4)}walk(o){return this.constructor._walk(o,this.rootNode)}static _walk(o,r){return typeof r=="string"?o.addText(r):r.children&&(o.openNode(r),r.children.forEach(y=>this._walk(o,y)),o.closeNode(r)),o}static _collapse(o){typeof o!="string"&&o.children&&(o.children.every(r=>typeof r=="string")?o.children=[o.children.join("")]:o.children.forEach(r=>{K._collapse(r)}))}}class ee extends K{constructor(o){super(),this.options=o}addText(o){o!==""&&this.add(o)}startScope(o){this.openNode(o)}endScope(){this.closeNode()}__addSublanguage(o,r){const y=o.root;r&&(y.scope=`language:${r}`),this.add(y)}toHTML(){return new M(this,this.options).value()}finalize(){return this.closeAllNodes(),!0}}function F(e){return e?typeof e=="string"?e:e.source:null}function L(e){return B("(?=",e,")")}function de(e){return B("(?:",e,")*")}function J(e){return B("(?:",e,")?")}function B(...e){return e.map(r=>F(r)).join("")}function ue(e){const o=e[e.length-1];return typeof o=="object"&&o.constructor===Object?(e.splice(e.length-1,1),o):{}}function ne(...e){return"("+(ue(e).capture?"":"?:")+e.map(y=>F(y)).join("|")+")"}function pe(e){return new RegExp(e.toString()+"|").exec("").length-1}function Ee(e,o){const r=e&&e.exec(o);return r&&r.index===0}const _e=/\[(?:[^\\\]]|\\.)*\]|\(\??|\\([1-9][0-9]*)|\\./;function ae(e,{joinWith:o}){let r=0;return e.map(y=>{r+=1;const P=r;let D=F(y),u="";for(;D.length>0;){const c=_e.exec(D);if(!c){u+=D;break}u+=D.substring(0,c.index),D=D.substring(c.index+c[0].length),c[0][0]==="\\"&&c[1]?u+="\\"+String(Number(c[1])+P):(u+=c[0],c[0]==="("&&r++)}return u}).map(y=>`(${y})`).join(o)}const be=/\b\B/,De="[a-zA-Z]\\w*",Se="[a-zA-Z_]\\w*",Le="\\b\\d+(\\.\\d+)?",Be="(-?)(\\b0[xX][a-fA-F0-9]+|(\\b\\d+(\\.\\d*)?|\\.\\d+)([eE][-+]?\\d+)?)",$e="\\b(0b[01]+)",Ve="!|!=|!==|%|%=|&|&&|&=|\\*|\\*=|\\+|\\+=|,|-|-=|/=|/|:|;|<<|<<=|<=|<|===|==|=|>>>=|>>=|>=|>>>|>>|>|\\?|\\[|\\{|\\(|\\^|\\^=|\\||\\|=|\\|\\||~",Ye=(e={})=>{const o=/^#![ ]*\//;return e.binary&&(e.begin=B(o,/.*\b/,e.binary,/\b.*/)),_({scope:"meta",begin:o,end:/$/,relevance:0,"on:begin":(r,y)=>{r.index!==0&&y.ignoreMatch()}},e)},fe={begin:"\\\\[\\s\\S]",relevance:0},Ze={scope:"string",begin:"'",end:"'",illegal:"\\n",contains:[fe]},je={scope:"string",begin:'"',end:'"',illegal:"\\n",contains:[fe]},me={begin:/\b(a|an|the|are|I'm|isn't|don't|doesn't|won't|but|just|should|pretty|simply|enough|gonna|going|wtf|so|such|will|you|your|they|like|more)\b/},$=function(e,o,r={}){const y=_({scope:"comment",begin:e,end:o,contains:[]},r);y.contains.push({scope:"doctag",begin:"[ ]*(?=(TODO|FIXME|NOTE|BUG|OPTIMIZE|HACK|XXX):)",end:/(TODO|FIXME|NOTE|BUG|OPTIMIZE|HACK|XXX):/,excludeBegin:!0,relevance:0});const P=ne("I","a","is","so","us","to","at","if","in","it","on",/[A-Za-z]+['](d|ve|re|ll|t|s|n)/,/[A-Za-z]+[-][a-z]+/,/[A-Za-z][a-z]{2,}/);return y.contains.push({begin:B(/[ ]+/,"(",P,/[.]?[:]?([.][ ]|[ ])/,"){3}")}),y},ve=$("//","$"),te=$("/\\*","\\*/"),Te=$("#","$"),He={scope:"number",begin:Le,relevance:0},re={scope:"number",begin:Be,relevance:0},Ue={scope:"number",begin:$e,relevance:0},ze={scope:"regexp",begin:/\/(?=[^/\n]*\/)/,end:/\/[gimuy]*/,contains:[fe,{begin:/\[/,end:/\]/,relevance:0,contains:[fe]}]},dt={scope:"title",begin:De,relevance:0},Q={scope:"title",begin:Se,relevance:0},ut={begin:"\\.\\s*"+Se,relevance:0};var Ke=Object.freeze({__proto__:null,APOS_STRING_MODE:Ze,BACKSLASH_ESCAPE:fe,BINARY_NUMBER_MODE:Ue,BINARY_NUMBER_RE:$e,COMMENT:$,C_BLOCK_COMMENT_MODE:te,C_LINE_COMMENT_MODE:ve,C_NUMBER_MODE:re,C_NUMBER_RE:Be,END_SAME_AS_BEGIN:function(e){return Object.assign(e,{"on:begin":(o,r)=>{r.data._beginMatch=o[1]},"on:end":(o,r)=>{r.data._beginMatch!==o[1]&&r.ignoreMatch()}})},HASH_COMMENT_MODE:Te,IDENT_RE:De,MATCH_NOTHING_RE:be,METHOD_GUARD:ut,NUMBER_MODE:He,NUMBER_RE:Le,PHRASAL_WORDS_MODE:me,QUOTE_STRING_MODE:je,REGEXP_MODE:ze,RE_STARTERS_RE:Ve,SHEBANG:Ye,TITLE_MODE:dt,UNDERSCORE_IDENT_RE:Se,UNDERSCORE_TITLE_MODE:Q});function ht(e,o){e.input[e.index-1]==="."&&o.ignoreMatch()}function st(e,o){e.className!==void 0&&(e.scope=e.className,delete e.className)}function G(e,o){o&&e.beginKeywords&&(e.begin="\\b("+e.beginKeywords.split(" ").join("|")+")(?!\\.)(?=\\b|\\s)",e.__beforeBegin=ht,e.keywords=e.keywords||e.beginKeywords,delete e.beginKeywords,e.relevance===void 0&&(e.relevance=0))}function ie(e,o){Array.isArray(e.illegal)&&(e.illegal=ne(...e.illegal))}function Fe(e,o){if(e.match){if(e.begin||e.end)throw new Error("begin & end are not supported with match");e.begin=e.match,delete e.match}}function h(e,o){e.relevance===void 0&&(e.relevance=1)}const s=(e,o)=>{if(!e.beforeMatch)return;if(e.starts)throw new Error("beforeMatch cannot be used with starts");const r=Object.assign({},e);Object.keys(e).forEach(y=>{delete e[y]}),e.keywords=r.keywords,e.begin=B(r.beforeMatch,L(r.begin)),e.starts={relevance:0,contains:[Object.assign(r,{endsParent:!0})]},e.relevance=0,delete r.beforeMatch},T=["of","and","for","in","not","or","if","then","parent","list","value"],d="keyword";function N(e,o,r=d){const y=Object.create(null);return typeof e=="string"?P(r,e.split(" ")):Array.isArray(e)?P(r,e):Object.keys(e).forEach(function(D){Object.assign(y,N(e[D],o,D))}),y;function P(D,u){o&&(u=u.map(c=>c.toLowerCase())),u.forEach(function(c){const v=c.split("|");y[v[0]]=[D,ce(v[0],v[1])]})}}function ce(e,o){return o?Number(o):gt(e)?0:1}function gt(e){return T.includes(e.toLowerCase())}const C={},xe=e=>{console.error(e)},Oe=(e,...o)=>{console.log(`WARN: ${e}`,...o)},Je=(e,o)=>{C[`${e}/${o}`]||(console.log(`Deprecated as of ${e}. ${o}`),C[`${e}/${o}`]=!0)},ot=new Error;function Ct(e,o,{key:r}){let y=0;const P=e[r],D={},u={};for(let c=1;c<=o.length;c++)u[c+y]=P[c],D[c+y]=!0,y+=pe(o[c-1]);e[r]=u,e[r]._emit=D,e[r]._multi=!0}function as(e){if(Array.isArray(e.begin)){if(e.skip||e.excludeBegin||e.returnBegin)throw xe("skip, excludeBegin, returnBegin not compatible with beginScope: {}"),ot;if(typeof e.beginScope!="object"||e.beginScope===null)throw xe("beginScope must be object"),ot;Ct(e,e.begin,{key:"beginScope"}),e.begin=ae(e.begin,{joinWith:""})}}function rs(e){if(Array.isArray(e.end)){if(e.skip||e.excludeEnd||e.returnEnd)throw xe("skip, excludeEnd, returnEnd not compatible with endScope: {}"),ot;if(typeof e.endScope!="object"||e.endScope===null)throw xe("endScope must be object"),ot;Ct(e,e.end,{key:"endScope"}),e.end=ae(e.end,{joinWith:""})}}function is(e){e.scope&&typeof e.scope=="object"&&e.scope!==null&&(e.beginScope=e.scope,delete e.scope)}function cs(e){is(e),typeof e.beginScope=="string"&&(e.beginScope={_wrap:e.beginScope}),typeof e.endScope=="string"&&(e.endScope={_wrap:e.endScope}),as(e),rs(e)}function ls(e){function o(u,c){return new RegExp(F(u),"m"+(e.case_insensitive?"i":"")+(e.unicodeRegex?"u":"")+(c?"g":""))}class r{constructor(){this.matchIndexes={},this.regexes=[],this.matchAt=1,this.position=0}addRule(c,v){v.position=this.position++,this.matchIndexes[this.matchAt]=v,this.regexes.push([v,c]),this.matchAt+=pe(c)+1}compile(){this.regexes.length===0&&(this.exec=()=>null);const c=this.regexes.map(v=>v[1]);this.matcherRe=o(ae(c,{joinWith:"|"}),!0),this.lastIndex=0}exec(c){this.matcherRe.lastIndex=this.lastIndex;const v=this.matcherRe.exec(c);if(!v)return null;const q=v.findIndex((et,ft)=>ft>0&&et!==void 0),j=this.matchIndexes[q];return v.splice(0,q),Object.assign(v,j)}}class y{constructor(){this.rules=[],this.multiRegexes=[],this.count=0,this.lastIndex=0,this.regexIndex=0}getMatcher(c){if(this.multiRegexes[c])return this.multiRegexes[c];const v=new r;return this.rules.slice(c).forEach(([q,j])=>v.addRule(q,j)),v.compile(),this.multiRegexes[c]=v,v}resumingScanAtSamePosition(){return this.regexIndex!==0}considerAll(){this.regexIndex=0}addRule(c,v){this.rules.push([c,v]),v.type==="begin"&&this.count++}exec(c){const v=this.getMatcher(this.regexIndex);v.lastIndex=this.lastIndex;let q=v.exec(c);if(this.resumingScanAtSamePosition()&&!(q&&q.index===this.lastIndex)){const j=this.getMatcher(0);j.lastIndex=this.lastIndex+1,q=j.exec(c)}return q&&(this.regexIndex+=q.position+1,this.regexIndex===this.count&&this.considerAll()),q}}function P(u){const c=new y;return u.contains.forEach(v=>c.addRule(v.begin,{rule:v,type:"begin"})),u.terminatorEnd&&c.addRule(u.terminatorEnd,{type:"end"}),u.illegal&&c.addRule(u.illegal,{type:"illegal"}),c}function D(u,c){const v=u;if(u.isCompiled)return v;[st,Fe,cs,s].forEach(j=>j(u,c)),e.compilerExtensions.forEach(j=>j(u,c)),u.__beforeBegin=null,[G,ie,h].forEach(j=>j(u,c)),u.isCompiled=!0;let q=null;return typeof u.keywords=="object"&&u.keywords.$pattern&&(u.keywords=Object.assign({},u.keywords),q=u.keywords.$pattern,delete u.keywords.$pattern),q=q||/\w+/,u.keywords&&(u.keywords=N(u.keywords,e.case_insensitive)),v.keywordPatternRe=o(q,!0),c&&(u.begin||(u.begin=/\B|\b/),v.beginRe=o(v.begin),!u.end&&!u.endsWithParent&&(u.end=/\B|\b/),u.end&&(v.endRe=o(v.end)),v.terminatorEnd=F(v.end)||"",u.endsWithParent&&c.terminatorEnd&&(v.terminatorEnd+=(u.end?"|":"")+c.terminatorEnd)),u.illegal&&(v.illegalRe=o(u.illegal)),u.contains||(u.contains=[]),u.contains=[].concat(...u.contains.map(function(j){return ds(j==="self"?u:j)})),u.contains.forEach(function(j){D(j,v)}),u.starts&&D(u.starts,c),v.matcher=P(v),v}if(e.compilerExtensions||(e.compilerExtensions=[]),e.contains&&e.contains.includes("self"))throw new Error("ERR: contains `self` is not supported at the top-level of a language.  See documentation.");return e.classNameAliases=_(e.classNameAliases||{}),D(e)}function At(e){return e?e.endsWithParent||At(e.starts):!1}function ds(e){return e.variants&&!e.cachedVariants&&(e.cachedVariants=e.variants.map(function(o){return _(e,{variants:null},o)})),e.cachedVariants?e.cachedVariants:At(e)?_(e,{starts:e.starts?_(e.starts):null}):Object.isFrozen(e)?_(e):e}var us="11.11.1";class ps extends Error{constructor(o,r){super(o),this.name="HTMLInjectionError",this.html=r}}const bt=f,Ot=_,It=Symbol("nomatch"),hs=7,Rt=function(e){const o=Object.create(null),r=Object.create(null),y=[];let P=!0;const D="Could not find the language '{}', did you forget to load/include a language module?",u={disableAutodetect:!0,name:"Plain text",contains:[]};let c={ignoreUnescapedHTML:!1,throwUnescapedHTML:!1,noHighlightRe:/^(no-?highlight)$/i,languageDetectRe:/\blang(?:uage)?-([\w-]+)\b/i,classPrefix:"hljs-",cssSelector:"pre code",languages:null,__emitter:ee};function v(n){return c.noHighlightRe.test(n)}function q(n){let g=n.className+" ";g+=n.parentNode?n.parentNode.className:"";const S=c.languageDetectRe.exec(g);if(S){const A=Ie(S[1]);return A||(Oe(D.replace("{}",S[1])),Oe("Falling back to no-highlight mode for this block.",n)),A?S[1]:"no-highlight"}return g.split(/\s+/).find(A=>v(A)||Ie(A))}function j(n,g,S){let A="",H="";typeof g=="object"?(A=n,S=g.ignoreIllegals,H=g.language):(Je("10.7.0","highlight(lang, code, ...args) has been deprecated."),Je("10.7.0",`Please use highlight(code, options) instead.
https://github.com/highlightjs/highlight.js/issues/2277`),H=n,A=g),S===void 0&&(S=!0);const he={code:A,language:H};at("before:highlight",he);const Re=he.result?he.result:et(he.language,he.code,S);return Re.code=he.code,at("after:highlight",Re),Re}function et(n,g,S,A){const H=Object.create(null);function he(i,p){return i.keywords[p]}function Re(){if(!w.keywords){W.addText(O);return}let i=0;w.keywordPatternRe.lastIndex=0;let p=w.keywordPatternRe.exec(O),E="";for(;p;){E+=O.substring(i,p.index);const x=we.case_insensitive?p[0].toLowerCase():p[0],V=he(w,x);if(V){const[Ce,Is]=V;if(W.addText(E),E="",H[x]=(H[x]||0)+1,H[x]<=hs&&(ct+=Is),Ce.startsWith("_"))E+=p[0];else{const Rs=we.classNameAliases[Ce]||Ce;ye(p[0],Rs)}}else E+=p[0];i=w.keywordPatternRe.lastIndex,p=w.keywordPatternRe.exec(O)}E+=O.substring(i),W.addText(E)}function rt(){if(O==="")return;let i=null;if(typeof w.subLanguage=="string"){if(!o[w.subLanguage]){W.addText(O);return}i=et(w.subLanguage,O,!0,jt[w.subLanguage]),jt[w.subLanguage]=i._top}else i=mt(O,w.subLanguage.length?w.subLanguage:null);w.relevance>0&&(ct+=i.relevance),W.__addSublanguage(i._emitter,i.language)}function oe(){w.subLanguage!=null?rt():Re(),O=""}function ye(i,p){i!==""&&(W.startScope(p),W.addText(i),W.endScope())}function Dt(i,p){let E=1;const x=p.length-1;for(;E<=x;){if(!i._emit[E]){E++;continue}const V=we.classNameAliases[i[E]]||i[E],Ce=p[E];V?ye(Ce,V):(O=Ce,Re(),O=""),E++}}function Lt(i,p){return i.scope&&typeof i.scope=="string"&&W.openNode(we.classNameAliases[i.scope]||i.scope),i.beginScope&&(i.beginScope._wrap?(ye(O,we.classNameAliases[i.beginScope._wrap]||i.beginScope._wrap),O=""):i.beginScope._multi&&(Dt(i.beginScope,p),O="")),w=Object.create(i,{parent:{value:w}}),w}function Bt(i,p,E){let x=Ee(i.endRe,E);if(x){if(i["on:end"]){const V=new R(i);i["on:end"](p,V),V.isMatchIgnored&&(x=!1)}if(x){for(;i.endsParent&&i.parent;)i=i.parent;return i}}if(i.endsWithParent)return Bt(i.parent,p,E)}function Ts(i){return w.matcher.regexIndex===0?(O+=i[0],1):(kt=!0,0)}function xs(i){const p=i[0],E=i.rule,x=new R(E),V=[E.__beforeBegin,E["on:begin"]];for(const Ce of V)if(Ce&&(Ce(i,x),x.isMatchIgnored))return Ts(p);return E.skip?O+=p:(E.excludeBegin&&(O+=p),oe(),!E.returnBegin&&!E.excludeBegin&&(O=p)),Lt(E,i),E.returnBegin?0:p.length}function Cs(i){const p=i[0],E=g.substring(i.index),x=Bt(w,i,E);if(!x)return It;const V=w;w.endScope&&w.endScope._wrap?(oe(),ye(p,w.endScope._wrap)):w.endScope&&w.endScope._multi?(oe(),Dt(w.endScope,i)):V.skip?O+=p:(V.returnEnd||V.excludeEnd||(O+=p),oe(),V.excludeEnd&&(O=p));do w.scope&&W.closeNode(),!w.skip&&!w.subLanguage&&(ct+=w.relevance),w=w.parent;while(w!==x.parent);return x.starts&&Lt(x.starts,i),V.returnEnd?0:p.length}function As(){const i=[];for(let p=w;p!==we;p=p.parent)p.scope&&i.unshift(p.scope);i.forEach(p=>W.openNode(p))}let it={};function $t(i,p){const E=p&&p[0];if(O+=i,E==null)return oe(),0;if(it.type==="begin"&&p.type==="end"&&it.index===p.index&&E===""){if(O+=g.slice(p.index,p.index+1),!P){const x=new Error(`0 width match regex (${n})`);throw x.languageName=n,x.badRule=it.rule,x}return 1}if(it=p,p.type==="begin")return xs(p);if(p.type==="illegal"&&!S){const x=new Error('Illegal lexeme "'+E+'" for mode "'+(w.scope||"<unnamed>")+'"');throw x.mode=w,x}else if(p.type==="end"){const x=Cs(p);if(x!==It)return x}if(p.type==="illegal"&&E==="")return O+=`
`,1;if(wt>1e5&&wt>p.index*3)throw new Error("potential infinite loop, way more iterations than matches");return O+=E,E.length}const we=Ie(n);if(!we)throw xe(D.replace("{}",n)),new Error('Unknown language: "'+n+'"');const Os=ls(we);let yt="",w=A||Os;const jt={},W=new c.__emitter(c);As();let O="",ct=0,Ge=0,wt=0,kt=!1;try{if(we.__emitTokens)we.__emitTokens(g,W);else{for(w.matcher.considerAll();;){wt++,kt?kt=!1:w.matcher.considerAll(),w.matcher.lastIndex=Ge;const i=w.matcher.exec(g);if(!i)break;const p=g.substring(Ge,i.index),E=$t(p,i);Ge=i.index+E}$t(g.substring(Ge))}return W.finalize(),yt=W.toHTML(),{language:n,value:yt,relevance:ct,illegal:!1,_emitter:W,_top:w}}catch(i){if(i.message&&i.message.includes("Illegal"))return{language:n,value:bt(g),illegal:!0,relevance:0,_illegalBy:{message:i.message,index:Ge,context:g.slice(Ge-100,Ge+100),mode:i.mode,resultSoFar:yt},_emitter:W};if(P)return{language:n,value:bt(g),illegal:!1,relevance:0,errorRaised:i,_emitter:W,_top:w};throw i}}function ft(n){const g={value:bt(n),illegal:!1,relevance:0,_top:u,_emitter:new c.__emitter(c)};return g._emitter.addText(n),g}function mt(n,g){g=g||c.languages||Object.keys(o);const S=ft(n),A=g.filter(Ie).filter(Pt).map(oe=>et(oe,n,!1));A.unshift(S);const H=A.sort((oe,ye)=>{if(oe.relevance!==ye.relevance)return ye.relevance-oe.relevance;if(oe.language&&ye.language){if(Ie(oe.language).supersetOf===ye.language)return 1;if(Ie(ye.language).supersetOf===oe.language)return-1}return 0}),[he,Re]=H,rt=he;return rt.secondBest=Re,rt}function gs(n,g,S){const A=g&&r[g]||S;n.classList.add("hljs"),n.classList.add(`language-${A}`)}function vt(n){let g=null;const S=q(n);if(v(S))return;if(at("before:highlightElement",{el:n,language:S}),n.dataset.highlighted){console.log("Element previously highlighted. To highlight again, first unset `dataset.highlighted`.",n);return}if(n.children.length>0&&(c.ignoreUnescapedHTML||(console.warn("One of your code blocks includes unescaped HTML. This is a potentially serious security risk."),console.warn("https://github.com/highlightjs/highlight.js/wiki/security"),console.warn("The element with unescaped HTML:"),console.warn(n)),c.throwUnescapedHTML))throw new ps("One of your code blocks includes unescaped HTML.",n.innerHTML);g=n;const A=g.textContent,H=S?j(A,{language:S,ignoreIllegals:!0}):mt(A);n.innerHTML=H.value,n.dataset.highlighted="yes",gs(n,S,H.language),n.result={language:H.language,re:H.relevance,relevance:H.relevance},H.secondBest&&(n.secondBest={language:H.secondBest.language,relevance:H.secondBest.relevance}),at("after:highlightElement",{el:n,result:H,text:A})}function bs(n){c=Ot(c,n)}const fs=()=>{nt(),Je("10.6.0","initHighlighting() deprecated.  Use highlightAll() now.")};function ms(){nt(),Je("10.6.0","initHighlightingOnLoad() deprecated.  Use highlightAll() now.")}let Nt=!1;function nt(){function n(){nt()}if(document.readyState==="loading"){Nt||window.addEventListener("DOMContentLoaded",n,!1),Nt=!0;return}document.querySelectorAll(c.cssSelector).forEach(vt)}function vs(n,g){let S=null;try{S=g(e)}catch(A){if(xe("Language definition for '{}' could not be registered.".replace("{}",n)),P)xe(A);else throw A;S=u}S.name||(S.name=n),o[n]=S,S.rawDefinition=g.bind(null,e),S.aliases&&Mt(S.aliases,{languageName:n})}function ys(n){delete o[n];for(const g of Object.keys(r))r[g]===n&&delete r[g]}function ws(){return Object.keys(o)}function Ie(n){return n=(n||"").toLowerCase(),o[n]||o[r[n]]}function Mt(n,{languageName:g}){typeof n=="string"&&(n=[n]),n.forEach(S=>{r[S.toLowerCase()]=g})}function Pt(n){const g=Ie(n);return g&&!g.disableAutodetect}function ks(n){n["before:highlightBlock"]&&!n["before:highlightElement"]&&(n["before:highlightElement"]=g=>{n["before:highlightBlock"](Object.assign({block:g.el},g))}),n["after:highlightBlock"]&&!n["after:highlightElement"]&&(n["after:highlightElement"]=g=>{n["after:highlightBlock"](Object.assign({block:g.el},g))})}function Es(n){ks(n),y.push(n)}function _s(n){const g=y.indexOf(n);g!==-1&&y.splice(g,1)}function at(n,g){const S=n;y.forEach(function(A){A[S]&&A[S](g)})}function Ss(n){return Je("10.7.0","highlightBlock will be removed entirely in v12.0"),Je("10.7.0","Please use highlightElement now."),vt(n)}Object.assign(e,{highlight:j,highlightAuto:mt,highlightAll:nt,highlightElement:vt,highlightBlock:Ss,configure:bs,initHighlighting:fs,initHighlightingOnLoad:ms,registerLanguage:vs,unregisterLanguage:ys,listLanguages:ws,getLanguage:Ie,registerAliases:Mt,autoDetection:Pt,inherit:Ot,addPlugin:Es,removePlugin:_s}),e.debugMode=function(){P=!1},e.safeMode=function(){P=!0},e.versionString=us,e.regex={concat:B,lookahead:L,either:ne,optional:J,anyNumberOfTimes:de};for(const n in Ke)typeof Ke[n]=="object"&&l(Ke[n]);return Object.assign(e,Ke),e},Qe=Rt({});return Qe.newInstance=()=>Rt({}),Tt=Qe,Qe.HighlightJS=Qe,Qe.default=Qe,Tt}var Vs=Xs();const ke=Ws(Vs);function Ys(l){const R=l.regex,f=new RegExp("[\\p{XID_Start}_]\\p{XID_Continue}*","u"),_=["and","as","assert","async","await","break","case","class","continue","def","del","elif","else","except","finally","for","from","global","if","import","in","is","lambda","match","nonlocal|10","not","or","pass","raise","return","try","while","with","yield"],M={$pattern:/[A-Za-z]\w+|__\w+__/,keyword:_,built_in:["__import__","abs","all","any","ascii","bin","bool","breakpoint","bytearray","bytes","callable","chr","classmethod","compile","complex","delattr","dict","dir","divmod","enumerate","eval","exec","filter","float","format","frozenset","getattr","globals","hasattr","hash","help","hex","id","input","int","isinstance","issubclass","iter","len","list","locals","map","max","memoryview","min","next","object","oct","open","ord","pow","print","property","range","repr","reversed","round","set","setattr","slice","sorted","staticmethod","str","sum","super","tuple","type","vars","zip"],literal:["__debug__","Ellipsis","False","None","NotImplemented","True"],type:["Any","Callable","Coroutine","Dict","List","Literal","Generic","Optional","Sequence","Set","Tuple","Type","Union"]},z={className:"meta",begin:/^(>>>|\.\.\.) /},K={className:"subst",begin:/\{/,end:/\}/,keywords:M,illegal:/#/},ee={begin:/\{\{/,relevance:0},F={className:"string",contains:[l.BACKSLASH_ESCAPE],variants:[{begin:/([uU]|[bB]|[rR]|[bB][rR]|[rR][bB])?'''/,end:/'''/,contains:[l.BACKSLASH_ESCAPE,z],relevance:10},{begin:/([uU]|[bB]|[rR]|[bB][rR]|[rR][bB])?"""/,end:/"""/,contains:[l.BACKSLASH_ESCAPE,z],relevance:10},{begin:/([fF][rR]|[rR][fF]|[fF])'''/,end:/'''/,contains:[l.BACKSLASH_ESCAPE,z,ee,K]},{begin:/([fF][rR]|[rR][fF]|[fF])"""/,end:/"""/,contains:[l.BACKSLASH_ESCAPE,z,ee,K]},{begin:/([uU]|[rR])'/,end:/'/,relevance:10},{begin:/([uU]|[rR])"/,end:/"/,relevance:10},{begin:/([bB]|[bB][rR]|[rR][bB])'/,end:/'/},{begin:/([bB]|[bB][rR]|[rR][bB])"/,end:/"/},{begin:/([fF][rR]|[rR][fF]|[fF])'/,end:/'/,contains:[l.BACKSLASH_ESCAPE,ee,K]},{begin:/([fF][rR]|[rR][fF]|[fF])"/,end:/"/,contains:[l.BACKSLASH_ESCAPE,ee,K]},l.APOS_STRING_MODE,l.QUOTE_STRING_MODE]},L="[0-9](_?[0-9])*",de=`(\\b(${L}))?\\.(${L})|\\b(${L})\\.`,J=`\\b|${_.join("|")}`,B={className:"number",relevance:0,variants:[{begin:`(\\b(${L})|(${de}))[eE][+-]?(${L})[jJ]?(?=${J})`},{begin:`(${de})[jJ]?`},{begin:`\\b([1-9](_?[0-9])*|0+(_?0)*)[lLjJ]?(?=${J})`},{begin:`\\b0[bB](_?[01])+[lL]?(?=${J})`},{begin:`\\b0[oO](_?[0-7])+[lL]?(?=${J})`},{begin:`\\b0[xX](_?[0-9a-fA-F])+[lL]?(?=${J})`},{begin:`\\b(${L})[jJ](?=${J})`}]},ue={className:"comment",begin:R.lookahead(/# type:/),end:/$/,keywords:M,contains:[{begin:/# type:/},{begin:/#/,end:/\b\B/,endsWithParent:!0}]},ne={className:"params",variants:[{className:"",begin:/\(\s*\)/,skip:!0},{begin:/\(/,end:/\)/,excludeBegin:!0,excludeEnd:!0,keywords:M,contains:["self",z,B,F,l.HASH_COMMENT_MODE]}]};return K.contains=[F,B,z],{name:"Python",aliases:["py","gyp","ipython"],unicodeRegex:!0,keywords:M,illegal:/(<\/|\?)|=>/,contains:[z,B,{scope:"variable.language",match:/\bself\b/},{beginKeywords:"if",relevance:0},{match:/\bor\b/,scope:"keyword"},F,ue,l.HASH_COMMENT_MODE,{match:[/\bdef/,/\s+/,f],scope:{1:"keyword",3:"title.function"},contains:[ne]},{variants:[{match:[/\bclass/,/\s+/,f,/\s*/,/\(\s*/,f,/\s*\)/]},{match:[/\bclass/,/\s+/,f]}],scope:{1:"keyword",3:"title.class",6:"title.class.inherited"}},{className:"meta",begin:/^[\t ]*@/,end:/(?=#)|$/,contains:[B,ne,F]}]}}const Kt="[A-Za-z$_][0-9A-Za-z$_]*",Zs=["as","in","of","if","for","while","finally","var","new","function","do","return","void","else","break","catch","instanceof","with","throw","case","default","try","switch","continue","typeof","delete","let","yield","const","class","debugger","async","await","static","import","from","export","extends","using"],Js=["true","false","null","undefined","NaN","Infinity"],ss=["Object","Function","Boolean","Symbol","Math","Date","Number","BigInt","String","RegExp","Array","Float32Array","Float64Array","Int8Array","Uint8Array","Uint8ClampedArray","Int16Array","Int32Array","Uint16Array","Uint32Array","BigInt64Array","BigUint64Array","Set","Map","WeakSet","WeakMap","ArrayBuffer","SharedArrayBuffer","Atomics","DataView","JSON","Promise","Generator","GeneratorFunction","AsyncFunction","Reflect","Proxy","Intl","WebAssembly"],os=["Error","EvalError","InternalError","RangeError","ReferenceError","SyntaxError","TypeError","URIError"],ns=["setInterval","setTimeout","clearInterval","clearTimeout","require","exports","eval","isFinite","isNaN","parseFloat","parseInt","decodeURI","decodeURIComponent","encodeURI","encodeURIComponent","escape","unescape"],Qs=["arguments","this","super","console","window","document","localStorage","sessionStorage","module","global"],eo=[].concat(ns,ss,os);function Ft(l){const R=l.regex,f=($,{after:ve})=>{const te="</"+$[0].slice(1);return $.input.indexOf(te,ve)!==-1},_=Kt,X={begin:"<>",end:"</>"},Z=/<[A-Za-z0-9\\._:-]+\s*\/>/,se={begin:/<[A-Za-z0-9\\._:-]+/,end:/\/[A-Za-z0-9\\._:-]+>|\/>/,isTrulyOpeningTag:($,ve)=>{const te=$[0].length+$.index,Te=$.input[te];if(Te==="<"||Te===","){ve.ignoreMatch();return}Te===">"&&(f($,{after:te})||ve.ignoreMatch());let He;const re=$.input.substring(te);if(He=re.match(/^\s*=/)){ve.ignoreMatch();return}if((He=re.match(/^\s+extends\s+/))&&He.index===0){ve.ignoreMatch();return}}},M={$pattern:Kt,keyword:Zs,literal:Js,built_in:eo,"variable.language":Qs},z="[0-9](_?[0-9])*",K=`\\.(${z})`,ee="0|[1-9](_?[0-9])*|0[0-7]*[89][0-9]*",F={className:"number",variants:[{begin:`(\\b(${ee})((${K})|\\.)?|(${K}))[eE][+-]?(${z})\\b`},{begin:`\\b(${ee})\\b((${K})\\b|\\.)?|(${K})\\b`},{begin:"\\b(0|[1-9](_?[0-9])*)n\\b"},{begin:"\\b0[xX][0-9a-fA-F](_?[0-9a-fA-F])*n?\\b"},{begin:"\\b0[bB][0-1](_?[0-1])*n?\\b"},{begin:"\\b0[oO][0-7](_?[0-7])*n?\\b"},{begin:"\\b0[0-7]+n?\\b"}],relevance:0},L={className:"subst",begin:"\\$\\{",end:"\\}",keywords:M,contains:[]},de={begin:".?html`",end:"",starts:{end:"`",returnEnd:!1,contains:[l.BACKSLASH_ESCAPE,L],subLanguage:"xml"}},J={begin:".?css`",end:"",starts:{end:"`",returnEnd:!1,contains:[l.BACKSLASH_ESCAPE,L],subLanguage:"css"}},B={begin:".?gql`",end:"",starts:{end:"`",returnEnd:!1,contains:[l.BACKSLASH_ESCAPE,L],subLanguage:"graphql"}},ue={className:"string",begin:"`",end:"`",contains:[l.BACKSLASH_ESCAPE,L]},pe={className:"comment",variants:[l.COMMENT(/\/\*\*(?!\/)/,"\\*/",{relevance:0,contains:[{begin:"(?=@[A-Za-z]+)",relevance:0,contains:[{className:"doctag",begin:"@[A-Za-z]+"},{className:"type",begin:"\\{",end:"\\}",excludeEnd:!0,excludeBegin:!0,relevance:0},{className:"variable",begin:_+"(?=\\s*(-)|$)",endsParent:!0,relevance:0},{begin:/(?=[^\n])\s/,relevance:0}]}]}),l.C_BLOCK_COMMENT_MODE,l.C_LINE_COMMENT_MODE]},Ee=[l.APOS_STRING_MODE,l.QUOTE_STRING_MODE,de,J,B,ue,{match:/\$\d+/},F];L.contains=Ee.concat({begin:/\{/,end:/\}/,keywords:M,contains:["self"].concat(Ee)});const _e=[].concat(pe,L.contains),ae=_e.concat([{begin:/(\s*)\(/,end:/\)/,keywords:M,contains:["self"].concat(_e)}]),be={className:"params",begin:/(\s*)\(/,end:/\)/,excludeBegin:!0,excludeEnd:!0,keywords:M,contains:ae},De={variants:[{match:[/class/,/\s+/,_,/\s+/,/extends/,/\s+/,R.concat(_,"(",R.concat(/\./,_),")*")],scope:{1:"keyword",3:"title.class",5:"keyword",7:"title.class.inherited"}},{match:[/class/,/\s+/,_],scope:{1:"keyword",3:"title.class"}}]},Se={relevance:0,match:R.either(/\bJSON/,/\b[A-Z][a-z]+([A-Z][a-z]*|\d)*/,/\b[A-Z]{2,}([A-Z][a-z]+|\d)+([A-Z][a-z]*)*/,/\b[A-Z]{2,}[a-z]+([A-Z][a-z]+|\d)*([A-Z][a-z]*)*/),className:"title.class",keywords:{_:[...ss,...os]}},Le={label:"use_strict",className:"meta",relevance:10,begin:/^\s*['"]use (strict|asm)['"]/},Be={variants:[{match:[/function/,/\s+/,_,/(?=\s*\()/]},{match:[/function/,/\s*(?=\()/]}],className:{1:"keyword",3:"title.function"},label:"func.def",contains:[be],illegal:/%/},$e={relevance:0,match:/\b[A-Z][A-Z_0-9]+\b/,className:"variable.constant"};function Ve($){return R.concat("(?!",$.join("|"),")")}const Ye={match:R.concat(/\b/,Ve([...ns,"super","import"].map($=>`${$}\\s*\\(`)),_,R.lookahead(/\s*\(/)),className:"title.function",relevance:0},fe={begin:R.concat(/\./,R.lookahead(R.concat(_,/(?![0-9A-Za-z$_(])/))),end:_,excludeBegin:!0,keywords:"prototype",className:"property",relevance:0},Ze={match:[/get|set/,/\s+/,_,/(?=\()/],className:{1:"keyword",3:"title.function"},contains:[{begin:/\(\)/},be]},je="(\\([^()]*(\\([^()]*(\\([^()]*\\)[^()]*)*\\)[^()]*)*\\)|"+l.UNDERSCORE_IDENT_RE+")\\s*=>",me={match:[/const|var|let/,/\s+/,_,/\s*/,/=\s*/,/(async\s*)?/,R.lookahead(je)],keywords:"async",className:{1:"keyword",3:"title.function"},contains:[be]};return{name:"JavaScript",aliases:["js","jsx","mjs","cjs"],keywords:M,exports:{PARAMS_CONTAINS:ae,CLASS_REFERENCE:Se},illegal:/#(?![$_A-z])/,contains:[l.SHEBANG({label:"shebang",binary:"node",relevance:5}),Le,l.APOS_STRING_MODE,l.QUOTE_STRING_MODE,de,J,B,ue,pe,{match:/\$\d+/},F,Se,{scope:"attr",match:_+R.lookahead(":"),relevance:0},me,{begin:"("+l.RE_STARTERS_RE+"|\\b(case|return|throw)\\b)\\s*",keywords:"return throw case",relevance:0,contains:[pe,l.REGEXP_MODE,{className:"function",begin:je,returnBegin:!0,end:"\\s*=>",contains:[{className:"params",variants:[{begin:l.UNDERSCORE_IDENT_RE,relevance:0},{className:null,begin:/\(\s*\)/,skip:!0},{begin:/(\s*)\(/,end:/\)/,excludeBegin:!0,excludeEnd:!0,keywords:M,contains:ae}]}]},{begin:/,/,relevance:0},{match:/\s+/,relevance:0},{variants:[{begin:X.begin,end:X.end},{match:Z},{begin:se.begin,"on:begin":se.isTrulyOpeningTag,end:se.end}],subLanguage:"xml",contains:[{begin:se.begin,end:se.end,skip:!0,contains:["self"]}]}]},Be,{beginKeywords:"while if switch catch for"},{begin:"\\b(?!function)"+l.UNDERSCORE_IDENT_RE+"\\([^()]*(\\([^()]*(\\([^()]*\\)[^()]*)*\\)[^()]*)*\\)\\s*\\{",returnBegin:!0,label:"func.def",contains:[be,l.inherit(l.TITLE_MODE,{begin:_,className:"title.function"})]},{match:/\.\.\./,relevance:0},fe,{match:"\\$"+_,relevance:0},{match:[/\bconstructor(?=\s*\()/],className:{1:"title.function"},contains:[be]},Ye,$e,De,Ze,{match:/\$[(.]/}]}}function to(l){const R={className:"attr",begin:/"(\\.|[^\\"\r\n])*"(?=\s*:)/,relevance:1.01},f={match:/[{}[\],:]/,className:"punctuation",relevance:0},_=["true","false","null"],X={scope:"literal",beginKeywords:_.join(" ")};return{name:"JSON",aliases:["jsonc"],keywords:{literal:_},contains:[R,f,l.QUOTE_STRING_MODE,X,l.C_NUMBER_MODE,l.C_LINE_COMMENT_MODE,l.C_BLOCK_COMMENT_MODE],illegal:"\\S"}}function xt(l){const R=l.regex,f={},_={begin:/\$\{/,end:/\}/,contains:["self",{begin:/:-/,contains:[f]}]};Object.assign(f,{className:"variable",variants:[{begin:R.concat(/\$[\w\d#@][\w\d_]*/,"(?![\\w\\d])(?![$])")},_]});const X={className:"subst",begin:/\$\(/,end:/\)/,contains:[l.BACKSLASH_ESCAPE]},Z=l.inherit(l.COMMENT(),{match:[/(^|\s)/,/#.*$/],scope:{2:"comment"}}),se={begin:/<<-?\s*(?=\w+)/,starts:{contains:[l.END_SAME_AS_BEGIN({begin:/(\w+)/,end:/(\w+)/,className:"string"})]}},M={className:"string",begin:/"/,end:/"/,contains:[l.BACKSLASH_ESCAPE,f,X]};X.contains.push(M);const z={match:/\\"/},K={className:"string",begin:/'/,end:/'/},ee={match:/\\'/},F={begin:/\$?\(\(/,end:/\)\)/,contains:[{begin:/\d+#[0-9a-f]+/,className:"number"},l.NUMBER_MODE,f]},L=["fish","bash","zsh","sh","csh","ksh","tcsh","dash","scsh"],de=l.SHEBANG({binary:`(${L.join("|")})`,relevance:10}),J={className:"function",begin:/\w[\w\d_]*\s*\(\s*\)\s*\{/,returnBegin:!0,contains:[l.inherit(l.TITLE_MODE,{begin:/\w[\w\d_]*/})],relevance:0},B=["if","then","else","elif","fi","time","for","while","until","in","do","done","case","esac","coproc","function","select"],ue=["true","false"],ne={match:/(\/[a-z._-]+)+/},pe=["break","cd","continue","eval","exec","exit","export","getopts","hash","pwd","readonly","return","shift","test","times","trap","umask","unset"],Ee=["alias","bind","builtin","caller","command","declare","echo","enable","help","let","local","logout","mapfile","printf","read","readarray","source","sudo","type","typeset","ulimit","unalias"],_e=["autoload","bg","bindkey","bye","cap","chdir","clone","comparguments","compcall","compctl","compdescribe","compfiles","compgroups","compquote","comptags","comptry","compvalues","dirs","disable","disown","echotc","echoti","emulate","fc","fg","float","functions","getcap","getln","history","integer","jobs","kill","limit","log","noglob","popd","print","pushd","pushln","rehash","sched","setcap","setopt","stat","suspend","ttyctl","unfunction","unhash","unlimit","unsetopt","vared","wait","whence","where","which","zcompile","zformat","zftp","zle","zmodload","zparseopts","zprof","zpty","zregexparse","zsocket","zstyle","ztcp"],ae=["chcon","chgrp","chown","chmod","cp","dd","df","dir","dircolors","ln","ls","mkdir","mkfifo","mknod","mktemp","mv","realpath","rm","rmdir","shred","sync","touch","truncate","vdir","b2sum","base32","base64","cat","cksum","comm","csplit","cut","expand","fmt","fold","head","join","md5sum","nl","numfmt","od","paste","ptx","pr","sha1sum","sha224sum","sha256sum","sha384sum","sha512sum","shuf","sort","split","sum","tac","tail","tr","tsort","unexpand","uniq","wc","arch","basename","chroot","date","dirname","du","echo","env","expr","factor","groups","hostid","id","link","logname","nice","nohup","nproc","pathchk","pinky","printenv","printf","pwd","readlink","runcon","seq","sleep","stat","stdbuf","stty","tee","test","timeout","tty","uname","unlink","uptime","users","who","whoami","yes"];return{name:"Bash",aliases:["sh","zsh"],keywords:{$pattern:/\b[a-z][a-z0-9._-]+\b/,keyword:B,literal:ue,built_in:[...pe,...Ee,"set","shopt",..._e,...ae]},contains:[de,l.SHEBANG(),J,F,Z,se,ne,M,z,K,ee,f]}}function so(l){const R=l.regex,f="HTTP/([32]|1\\.[01])",_=/[A-Za-z][A-Za-z0-9-]*/,X={className:"attribute",begin:R.concat("^",_,"(?=\\:\\s)"),starts:{contains:[{className:"punctuation",begin:/: /,relevance:0,starts:{end:"$",relevance:0}}]}},Z=[X,{begin:"\\n\\n",starts:{subLanguage:[],endsWithParent:!0}}];return{name:"HTTP",aliases:["https"],illegal:/\S/,contains:[{begin:"^(?="+f+" \\d{3})",end:/$/,contains:[{className:"meta",begin:f},{className:"number",begin:"\\b\\d{3}\\b"}],starts:{end:/\b\B/,illegal:/\S/,contains:Z}},{begin:"(?=^[A-Z]+ (.*?) "+f+"$)",end:/$/,contains:[{className:"string",begin:" ",end:" ",excludeBegin:!0,excludeEnd:!0},{className:"meta",begin:f},{className:"keyword",begin:"[A-Z]+"}],starts:{end:/\b\B/,illegal:/\S/,contains:Z}},l.inherit(X,{relevance:0})]}}const oo={class:"space-y-12 pb-16"},no={class:"docs-hero"},ao={class:"docs-hero-content"},ro={class:"docs-hero-row"},io={class:"docs-hero-actions"},co=["aria-label"],lo=["aria-label"],uo={class:"docs-hero-toc","aria-label":"Jump to docs section"},po=["href"],ho={class:"docs-hero-toc-num"},go={id:"handler",class:"space-y-5 scroll-mt-6"},bo={class:"doc-table-wrap"},fo={class:"doc-table"},mo={class:"doc-cell-key"},vo={class:"doc-cell-mono"},yo={class:"doc-cell-mono hidden sm:table-cell"},wo={class:"doc-cell-mono hidden md:table-cell"},ko={id:"deploy",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Eo={class:"grid grid-cols-1 lg:grid-cols-2 gap-3"},_o={class:"space-y-2"},So={class:"space-y-2"},To={class:"space-y-2"},xo={id:"config",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Co={class:"doc-table-wrap"},Ao={class:"doc-table"},Oo={class:"doc-cell-key whitespace-nowrap"},Io={class:"doc-cell-mono hidden sm:table-cell whitespace-nowrap"},Ro={class:"doc-cell-body"},No={class:"space-y-2"},Mo={class:"doc-details group"},Po={class:"doc-details-summary"},Do={class:"doc-details-body"},Lo={id:"sdk",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Bo={class:"space-y-2"},$o={class:"space-y-2"},jo={class:"space-y-2"},Ho={id:"schedules",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Uo={class:"doc-section-head"},zo={class:"doc-lede"},Ko={id:"webhooks",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Fo={class:"doc-section-head"},Go={class:"doc-lede"},qo={class:"doc-table-wrap"},Wo={class:"doc-table"},Xo={class:"doc-cell-key whitespace-nowrap"},Vo={class:"doc-cell-body"},Yo={class:"space-y-2"},Zo={id:"mcp",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Jo={class:"grid grid-cols-1 md:grid-cols-3 gap-3"},Qo={class:"doc-card"},en={class:"doc-card-body"},tn={class:"doc-chip break-all"},sn={class:"doc-token-bar"},on={class:"flex items-center gap-2 min-w-0 flex-1"},nn={key:0,class:"text-sm text-foreground-muted truncate"},an={key:1,class:"text-sm text-success truncate"},rn={class:"doc-chip"},cn=["disabled"],ln={class:"doc-details group"},dn={class:"doc-details-summary"},un={class:"doc-details-body space-y-4"},pn={id:"generate",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},hn={class:"ai-prompt-actions"},gn={key:0,class:"prompt-collapse-fade","aria-hidden":"true"},bn=["aria-expanded"],fn={id:"tracing",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},mn={class:"doc-table-wrap"},vn={class:"doc-table"},yn={class:"doc-cell-key whitespace-nowrap"},wn={class:"doc-cell-body"},kn={id:"errors",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},En={class:"doc-table-wrap"},_n={class:"doc-table"},Sn={class:"doc-cell-key whitespace-nowrap"},Tn={class:"doc-cell-body"},xn={id:"cli",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Cn={class:"doc-prose"},An={class:"doc-table-wrap"},On={class:"doc-table"},In={class:"doc-cell-key whitespace-nowrap"},Rn={class:"doc-cell-mono"},Nn={class:"doc-cell-body hidden md:table-cell"},Mn={class:"space-y-2"},Pn={class:"space-y-2"},Dn={class:"space-y-2"},Ln={class:"space-y-2"},Bn={class:"space-y-2"},Gt=`# Available inside every running function — refresh per-invocation:
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
orva activity --source mcp --limit 200 # MCP-only, last 200`,ts="<YOUR_ORVA_TOKEN>",Wn={__name:"Docs",setup(l){const R=Ls();ke.registerLanguage("python",Ys),ke.registerLanguage("javascript",Ft),ke.registerLanguage("js",Ft),ke.registerLanguage("json",to),ke.registerLanguage("bash",xt),ke.registerLanguage("shell",xt),ke.registerLanguage("sh",xt),ke.registerLanguage("http",so);const f=Y(()=>window.location.origin),_=[{id:"handler",num:"01",label:"Handler"},{id:"deploy",num:"02",label:"Deploy"},{id:"config",num:"03",label:"Config"},{id:"sdk",num:"04",label:"SDK"},{id:"schedules",num:"05",label:"Schedules"},{id:"webhooks",num:"06",label:"Webhooks"},{id:"mcp",num:"07",label:"MCP"},{id:"generate",num:"08",label:"AI prompt"},{id:"tracing",num:"09",label:"Tracing"},{id:"errors",num:"10",label:"Errors"},{id:"cli",num:"11",label:"CLI"}],X=Ae("handler");let Z=null;Bs(()=>{if(typeof IntersectionObserver>"u")return;const h=new Set;Z=new IntersectionObserver(s=>{for(const T of s)T.isIntersecting?h.add(T.target.id):h.delete(T.target.id);for(const T of _)if(h.has(T.id)){X.value=T.id;break}},{rootMargin:"-20% 0px -70% 0px",threshold:0});for(const s of _){const T=document.getElementById(s.id);T&&Z.observe(T)}}),$s(()=>{Z&&Z.disconnect()});const se=Ps(),M=Ae(!1);let z=null;const K=async()=>{await Ms()&&(M.value=!0,clearTimeout(z),z=setTimeout(()=>{M.value=!1},1500))},ee=Pe({setup(){return()=>a("svg",{viewBox:"0 0 256 255",width:"14",height:"14",xmlns:"http://www.w3.org/2000/svg"},[a("defs",null,[a("linearGradient",{id:"pyg1",x1:"0",y1:"0",x2:"1",y2:"1"},[a("stop",{offset:"0","stop-color":"#387EB8"}),a("stop",{offset:"1","stop-color":"#366994"})]),a("linearGradient",{id:"pyg2",x1:"0",y1:"0",x2:"1",y2:"1"},[a("stop",{offset:"0","stop-color":"#FFE052"}),a("stop",{offset:"1","stop-color":"#FFC331"})])]),a("path",{fill:"url(#pyg1)",d:"M126.9 12c-58.3 0-54.7 25.3-54.7 25.3l.1 26.2H128v8H50.5S12 67.2 12 126.1c0 58.9 33.6 56.8 33.6 56.8h19.4v-27.4s-1-33.6 33.1-33.6h55.9s32 .5 32-30.9V43.5S191.7 12 126.9 12zM95.7 29.9a10 10 0 0 1 0 20 10 10 0 0 1 0-20z"}),a("path",{fill:"url(#pyg2)",d:"M129.1 243c58.3 0 54.7-25.3 54.7-25.3l-.1-26.2H128v-8h77.5s38.5 4.4 38.5-54.5c0-58.9-33.6-56.8-33.6-56.8h-19.4v27.4s1 33.6-33.1 33.6H102s-32-.5-32 30.9v52S64.3 243 129.1 243zm30.4-17.9a10 10 0 0 1 0-20 10 10 0 0 1 0 20z"})])}}),F=Pe({setup(){return()=>a("svg",{viewBox:"0 0 256 280",width:"14",height:"14",xmlns:"http://www.w3.org/2000/svg"},[a("path",{fill:"#3F873F",d:"M128 0 12 67v146l116 67 116-67V67L128 0zm0 24.6 95 54.8v121.2l-95 54.8-95-54.8V79.4l95-54.8z"}),a("path",{fill:"#3F873F",d:"M128 64c-3 0-5.7.7-8 2.3L73 92c-5 2.7-8 8-8 13.6V169c0 5.6 3 10.7 8 13.5l13 7.4c6.3 3.1 8.5 3.1 11.4 3.1 9.4 0 14.8-5.7 14.8-15.6V117c0-1-.7-1.7-1.7-1.7H103c-1 0-1.7.7-1.7 1.7v60.2c0 4.4-4.5 8.7-11.8 5.1l-13.7-7.9a1.6 1.6 0 0 1-.8-1.4v-63.4c0-.6.3-1 .8-1.4l46.8-26.9c.4-.3 1-.3 1.4 0L171 110c.5.4.8.8.8 1.4V174a1.7 1.7 0 0 1-.8 1.4l-46.8 27c-.4.2-1 .2-1.4 0l-12-7.2c-.4-.2-.8-.2-1.2 0-3.4 1.9-4 2.2-7.2 3.3-.8.3-2 .7.4 2.1l15.7 9.3c2.5 1.4 5.3 2.2 8.2 2.2 2.9 0 5.7-.8 8.2-2.2L181 184c5-2.8 8-7.9 8-13.5V107c0-5.6-3-10.7-8-13.5l-46.7-26.7a17 17 0 0 0-6.3-2.8z"})])}}),L=Pe({name:"DeployPipelineDiagram",setup(){const h=[{glyph:"▣",label:"Tarball",sub:"POST /deploy"},{glyph:"⟜",label:"Extract",sub:"untar → scratch dir"},{glyph:"◍",label:"Install",sub:"npm / pip"},{glyph:"⟐",label:"Compile",sub:"tsc (TypeScript)"},{glyph:"◉",label:"Activate",sub:"rename → current"},{glyph:"✦",label:"Warm pool",sub:"pre-spawn N workers"}];return()=>a("figure",{class:"doc-diagram"},[a("figcaption",{class:"doc-diagram-cap"},"Deploy pipeline"),a("div",{class:"doc-pipeline"},h.flatMap((s,T)=>{const d=a("div",{key:`s${T}`,class:"doc-pipeline-stage"},[a("div",{class:"doc-pipeline-glyph"},s.glyph),a("div",{class:"doc-pipeline-label"},[a("span",{class:"doc-pipeline-name"},s.label),a("span",{class:"doc-pipeline-sub"},s.sub)])]),N=T<h.length-1?a("div",{key:`a${T}`,class:"doc-pipeline-arrow","aria-hidden":"true"}):null;return N?[d,N]:[d]}))])}}),de=Pe({name:"TraceTreeDiagram",setup(){const s=[{fn:"api-gateway",trigger:"http",start:0,dur:220,parent:null,klass:"root"},{fn:"resize-image",trigger:"f2f",start:30,dur:90,parent:"api-gateway",klass:"child"},{fn:"send-email",trigger:"job",start:60,dur:40,parent:"api-gateway",klass:"grand"}],T=d=>d/220*100;return()=>a("figure",{class:"doc-diagram"},[a("figcaption",{class:"doc-diagram-cap"},"Causal trace — one HTTP request, three spans"),a("div",{class:"doc-trace"},[a("div",{class:"doc-trace-axis"},[a("span",null,"0 ms"),a("span",null,"220 ms")]),...s.map(d=>a("div",{key:d.fn,class:["doc-trace-row",`is-${d.klass}`]},[a("div",{class:"doc-trace-label"},[a("span",{class:"doc-trace-fn"},d.fn),a("span",{class:"doc-trace-trigger"},d.trigger)]),a("div",{class:"doc-trace-track"},[a("div",{class:"doc-trace-bar",style:{left:`${T(d.start)}%`,width:`${T(d.dur)}%`},title:`+${d.start}ms · ${d.dur}ms`})]),a("div",{class:"doc-trace-dur"},`${d.dur}ms`)])),a("div",{class:"doc-trace-legend"},[a("span",null,"Same "),a("code",{class:"doc-chip"},"trace_id"),a("span",null," across all spans · "),a("code",{class:"doc-chip"},"parent_span_id"),a("span",null," chains them into a tree.")])])])}}),J=Pe({name:"WebhookDeliveryDiagram",setup(){return()=>a("figure",{class:"doc-diagram"},[a("figcaption",{class:"doc-diagram-cap"},"Signed webhook delivery"),a("div",{class:"doc-webhook"},[a("div",{class:"doc-webhook-actor"},[a("div",{class:"doc-webhook-actor-head"},"orvad"),a("div",{class:"doc-webhook-actor-body"},[a("span",null,"event fires"),a("code",{class:"doc-chip"},"deployment.succeeded")])]),a("div",{class:"doc-webhook-wire"},[a("div",{class:"doc-webhook-wire-line","aria-hidden":"true"}),a("div",{class:"doc-webhook-wire-payload"},[a("div",{class:"doc-webhook-wire-method"},"POST"),a("div",{class:"doc-webhook-wire-headers"},[a("code",null,"X-Orva-Event"),a("code",null,"X-Orva-Timestamp"),a("code",null,"X-Orva-Signature")]),a("div",{class:"doc-webhook-wire-sig"},"sha256=hex(hmac(secret, ts.body))")])]),a("div",{class:"doc-webhook-actor"},[a("div",{class:"doc-webhook-actor-head"},"your receiver"),a("div",{class:"doc-webhook-actor-body"},[a("span",null,"verify HMAC"),a("span",null,"→ 2xx within 15s or get retried")])])])])}}),B=Y(()=>[{label:"Python",lang:"python",code:`def handler(event):
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
print(r.json())`}]),ne=[{id:"python314",name:"Python 3.14",entry:"handler.py",deps:"requirements.txt",icon:ee},{id:"python313",name:"Python 3.13",entry:"handler.py",deps:"requirements.txt",icon:ee},{id:"node24",name:"Node.js 24",entry:"handler.js",deps:"package.json",icon:F},{id:"node22",name:"Node.js 22",entry:"handler.js",deps:"package.json",icon:F}],pe=[{field:"env_vars",purpose:"Plain config",body:"Plaintext config stored on the function record. Use for feature flags and non-secret settings.",icon:Fs,iconClass:"text-violet-300"},{field:"/secrets",purpose:"Encrypted",body:"AES-256-GCM at rest. Values decrypt only into the worker environment at spawn time.",icon:tt,iconClass:"text-emerald-300"},{field:"network_mode",purpose:"Egress control",body:"none = isolated loopback. egress = outbound HTTPS allowed; firewall blocklist applies.",icon:St,iconClass:"text-sky-300"},{field:"auth_mode",purpose:"Invoke gate",body:"none = public. platform_key = require Orva API key. signed = require HMAC.",icon:Gs,iconClass:"text-violet-300"},{field:"rate_limit_per_min",purpose:"Per-IP throttle",body:"Optional cap for public or webhook-facing functions. Exceeding it returns 429.",icon:Ks,iconClass:"text-amber-300"}],Ee=Y(()=>`curl -X POST ${f.value}/api/v1/functions \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"name":"hello","runtime":"python314","memory_mb":128,"cpus":0.5}'`),_e=Y(()=>`tar czf code.tar.gz handler.py requirements.txt
curl -X POST ${f.value}/api/v1/functions/<function_id>/deploy \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -F code=@code.tar.gz`),ae=Y(()=>`curl -X POST ${f.value}/api/v1/functions/<function_id>/secrets \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"key":"DATABASE_URL","value":"postgres://..."}'`),be=Y(()=>`# generate signature
SECRET='your-shared-secret-stored-in-function-secrets'
TS=$(date +%s)
BODY='{"hello":"world"}'
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')

curl -X POST ${f.value}/fn/<function_id> \\
  -H "X-Orva-Timestamp: $TS" \\
  -H "X-Orva-Signature: sha256=$SIG" \\
  -H 'Content-Type: application/json' \\
  -d "$BODY"`),De=Y(()=>[{label:"curl",lang:"bash",note:"Create a daily-9am schedule for an existing function. payload is delivered as the invoke body.",code:`curl -X POST ${f.value}/api/v1/functions/<function_id>/cron \\
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
  -H 'X-Orva-API-Key: <YOUR_KEY>'`}]),Se=[{label:"Python",lang:"python",code:`from orva import kv

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
}`}],Le=[{label:"Python",lang:"python",code:`from orva import invoke, OrvaError

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
}`}],Be=[{label:"Python",lang:"python",code:`from orva import jobs

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
}`}],$e=[{name:"deployment.succeeded",when:"A function build finished and the new version is active."},{name:"deployment.failed",when:"A build failed or was rejected."},{name:"function.created",when:"A new function row was created via POST /api/v1/functions."},{name:"function.updated",when:"A function config was edited via PUT /api/v1/functions/{id} (status flips during a deploy do NOT fire this — see deployment.*)."},{name:"function.deleted",when:"A function was removed."},{name:"execution.error",when:"An invocation finished with status=error or 5xx."},{name:"cron.failed",when:"A scheduled run failed (bad expr, missing fn, dispatch error, or 5xx)."},{name:"job.succeeded",when:"A queued background job finished successfully."},{name:"job.failed",when:"A queued job exhausted its retries (terminal failure)."}],Ve=[{label:"Python",lang:"python",note:"Run on the receiver. Reject anything that fails verification — the signature ensures the request really came from this Orva instance.",code:`import hmac, hashlib, time

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
})`}],Ye=[{name:"http",desc:"Public HTTP request hit /fn/<id>/. Almost always a root span."},{name:"f2f",desc:"Another function called this one via orva.invoke(). Has a parent_span_id."},{name:"job",desc:"Background job runner picked up an enqueued job. Parent_span_id is whoever enqueued it."},{name:"cron",desc:"Scheduler fired a cron entry. Always a root span."},{name:"inbound",desc:"External webhook hit /webhook/{id}. Always a root span."},{name:"replay",desc:"Operator clicked Replay on a captured execution. Fresh trace, no link to original."},{name:"mcp",desc:"AI agent invoked the function via MCP invoke_function. Fresh trace."}],fe=[{code:"VALIDATION",when:"Bad request body or path parameter."},{code:"UNAUTHORIZED",when:"Missing or invalid API key / session cookie."},{code:"NOT_FOUND",when:"Function, deployment, or secret doesn't exist."},{code:"RATE_LIMITED",when:"Too many requests — check the Retry-After header."},{code:"VERSION_GCD",when:"Rollback target was garbage-collected."},{code:"INSUFFICIENT_DISK",when:"Host is below min_free_disk_mb."}],Ze=[{cmd:"login",subs:"—",purpose:"Save endpoint + API key to ~/.orva/config.yaml"},{cmd:"init",subs:"—",purpose:"Scaffold an orva.yaml in the current directory"},{cmd:"deploy",subs:"[path]",purpose:"Package a directory and deploy as a function"},{cmd:"invoke",subs:"[name|id]",purpose:"POST to /fn/<id>/ and print the response"},{cmd:"logs",subs:"[name|id] [--tail]",purpose:"List recent executions; --tail follows live via SSE"},{cmd:"functions",subs:"list / get / create / delete",purpose:"CRUD for the function registry"},{cmd:"cron",subs:"list / create / update / delete",purpose:"Manage cron schedules attached to functions"},{cmd:"jobs",subs:"list / enqueue / retry / delete",purpose:"Background queue management"},{cmd:"kv",subs:"list / get / put / delete",purpose:"Browse a function’s key/value store"},{cmd:"secrets",subs:"list / set / delete",purpose:"AES-256-GCM secrets per function"},{cmd:"webhooks",subs:"list / create / test / delete / inbound",purpose:"System-event subscribers + inbound triggers"},{cmd:"routes",subs:"list / set / delete",purpose:"Custom URL → function path mappings"},{cmd:"keys",subs:"list / create / revoke",purpose:"Manage API keys"},{cmd:"activity",subs:"[--tail] [--source web|api|...]",purpose:"Paginated activity rows; live SSE with --tail"},{cmd:"system",subs:"health / metrics / db-stats / vacuum",purpose:"Server diagnostics"},{cmd:"setup",subs:"[--skip-nsjail] [--skip-rootfs]",purpose:"Install nsjail + rootfs on a bare host"},{cmd:"serve",subs:"[--port N]",purpose:"Run as the server daemon (not the CLI client)"},{cmd:"completion",subs:"bash / zsh / fish / powershell",purpose:"Emit shell completion script"}],je=Y(()=>{const h=(C,xe)=>xe.map(Oe=>`### ${C} — ${Oe.label}

${Oe.note?`> ${Oe.note}

`:""}\`\`\`${Oe.lang}
${Oe.code}
\`\`\``).join(`

`),s=`| Runtime | ID | Entrypoint | Dependencies |
|---|---|---|---|
`+ne.map(C=>`| ${C.name} | \`${C.id}\` | \`${C.entry}\` | \`${C.deps}\` |`).join(`
`),T=`| Field | Purpose | Behaviour |
|---|---|---|
`+pe.map(C=>`| \`${C.field}\` | ${C.purpose} | ${C.body} |`).join(`
`),d=`| Trigger | Meaning |
|---|---|
`+Ye.map(C=>`| \`${C.name}\` | ${C.desc} |`).join(`
`),N=`| Event | When it fires |
|---|---|
`+$e.map(C=>`| \`${C.name}\` | ${C.when} |`).join(`
`),ce=`| Code | When you see it |
|---|---|
`+fe.map(C=>`| \`${C.code}\` | ${C.when} |`).join(`
`),gt=`| Command | Subcommands | Purpose |
|---|---|---|
`+Ze.map(C=>`| \`orva ${C.cmd}\` | ${C.subs} | ${C.purpose} |`).join(`
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

${h("Handler",B.value)}

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
${Ee.value}
\`\`\`

### 2. Upload code

\`\`\`bash
${_e.value}
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
${be.value}
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

${h("KV",Se)}

> Browse / inspect / edit / delete / set keys without leaving the
> dashboard at \`/web/functions/<name>/kv\`. REST mirror at
> \`GET/PUT/DELETE /api/v1/functions/<id>/kv[/<key>]\`. MCP tools:
> \`kv_list\` / \`kv_get\` / \`kv_put\` / \`kv_delete\`.

### Function-to-function — invoke()

${h("F2F",Le)}

### Background jobs — jobs.enqueue()

${h("Jobs",Be)}

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

${h("Cron",De.value)}

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

${h("Verify",Ve)}

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

${h("MCP",pt.value)}

### More clients (Cursor, VS Code, Codex CLI, OpenCode, Zed, Windsurf, ChatGPT)

${h("MCP (extra)",Ke.value)}

### Hand-edited config files

${h("MCP config",st.value)}

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

${gt}

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
`}),me=Ae(!1);let $=null;const ve=async()=>{await Et(je.value)&&(me.value=!0,clearTimeout($),$=setTimeout(()=>{me.value=!1},1500))},te=Ae(!1);let Te=null;const He=async()=>{await Et(je.value);const h=["I'm working with Orva, a self-hosted serverless platform.","I just copied the full Orva documentation to my clipboard — please ask me to paste it in my next message, then use it as the source of truth when answering my questions.","","After I paste, summarise back what Orva offers so I know you read it, then wait for my actual question."].join(`
`),s=`https://chatgpt.com/?q=${encodeURIComponent(h)}`;window.open(s,"_blank","noopener,noreferrer"),te.value=!0,clearTimeout(Te),Te=setTimeout(()=>{te.value=!1},2500)},re=Ae(!1),Ue=Ae(""),ze=Ae(!1),dt=Y(()=>Ue.value.slice(0,12)),Q=Y(()=>Ue.value||ts),ut=async()=>{if(!ze.value){ze.value=!0;try{const h=new Date().toISOString().slice(0,16).replace("T"," "),s=await zs.post("/keys",{name:"MCP — "+h,permissions:["invoke","read","write","admin"]});Ue.value=s.data.key}catch(h){console.error("mint mcp key failed",h),R.notify({title:"Could not mint key",message:h?.response?.data?.error?.message||h.message||"Unknown error",danger:!0})}finally{ze.value=!1}}},pt=Y(()=>[{label:"Claude Code",lang:"bash",note:"Anthropic's `claude` CLI. Restart Claude Code afterwards; `/mcp` lists Orva's 57 tools.",code:`claude mcp add --transport http --scope user orva ${f.value}/mcp --header "Authorization: Bearer ${Q.value}"`},{label:"curl",lang:"bash",note:"Talk to MCP directly. Step 1 returns a session id (Mcp-Session-Id) that Step 2 references.",code:`curl -sD - -X POST ${f.value}/mcp \\
  -H 'Authorization: Bearer ${Q.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'

curl -sX POST ${f.value}/mcp \\
  -H 'Authorization: Bearer ${Q.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H 'Mcp-Session-Id: <SID>' \\
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'`}]),Ke=Y(()=>[{label:"Claude Desktop",lang:"json",note:"Paste into ~/Library/Application Support/Claude/claude_desktop_config.json (macOS), %APPDATA%\\Claude\\claude_desktop_config.json (Windows), or ~/.config/Claude/claude_desktop_config.json (Linux). Restart Claude Desktop.",code:`{
  "mcpServers": {
    "orva": {
      "url": "${f.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${Q.value}"
      }
    }
  }
}`},{label:"Cursor",lang:"bash",note:"Open the link in your browser. Cursor pops an approval dialog and writes ~/.cursor/mcp.json.",code:`cursor://anysphere.cursor-deeplink/mcp/install?name=orva&config=${ht.value}`},{label:"VS Code",lang:"bash",note:'User-scoped install via the Copilot-MCP `code --add-mcp` flag. Pick "Workspace" at the prompt to write .vscode/mcp.json instead.',code:`code --add-mcp '{"name":"orva","type":"http","url":"${f.value}/mcp","headers":{"Authorization":"Bearer ${Q.value}"}}'`},{label:"Codex CLI",lang:"bash",note:"OpenAI's `codex` CLI. Writes to ~/.codex/config.toml.",code:`codex mcp add --transport streamable-http orva ${f.value}/mcp --header "Authorization: Bearer ${Q.value}"`},{label:"OpenCode",lang:"bash",note:`Interactive add. Pick "Remote", paste ${f.value}/mcp, then add the header Authorization: Bearer ${Q.value}.`,code:"opencode mcp add"},{label:"Zed",lang:"json",note:"Zed runs MCP as stdio subprocesses, so use the `mcp-remote` bridge. Paste under context_servers in ~/.config/zed/settings.json. Restart Zed.",code:`{
  "context_servers": {
    "orva": {
      "source": "custom",
      "command": "npx",
      "args": [
        "-y", "mcp-remote",
        "${f.value}/mcp",
        "--header", "Authorization:Bearer ${Q.value}"
      ]
    }
  }
}`},{label:"Windsurf",lang:"json",note:"Paste into ~/.codeium/windsurf/mcp_config.json and reload Windsurf.",code:`{
  "mcpServers": {
    "orva": {
      "serverUrl": "${f.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${Q.value}"
      }
    }
  }
}`},{label:"ChatGPT",lang:"text",note:"UI-only flow. Settings → Apps & Connectors → Developer mode → Add new connector. ChatGPT renders the tool catalog and confirms before destructive calls.",code:`URL:    ${f.value}/mcp
Auth:   API key (Bearer)
Token:  ${Q.value}`}]),ht=Y(()=>{const h=JSON.stringify({url:f.value+"/mcp",headers:{Authorization:"Bearer "+Q.value}});return typeof window.btoa=="function"?window.btoa(h):h}),st=Y(()=>[{label:"Cursor (global)",lang:"json",note:"Paste into ~/.cursor/mcp.json, or .cursor/mcp.json in your project root for a per-workspace install.",code:`{
  "mcpServers": {
    "orva": {
      "url": "${f.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${Q.value}"
      }
    }
  }
}`},{label:"Cline",lang:"json",note:"In VS Code: open Cline → MCP icon → Configure MCP Servers. Cline writes cline_mcp_settings.json.",code:`{
  "mcpServers": {
    "orva": {
      "url": "${f.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${Q.value}"
      },
      "disabled": false
    }
  }
}`}]),G=Pe({name:"CodeBlock",props:{code:{type:String,required:!0},lang:{type:String,default:""}},setup(h){const s=Ae(!1),T=async()=>{await Et(h.code)&&(s.value=!0,setTimeout(()=>{s.value=!1},1200))},d=Y(()=>{const N=(h.lang||"").toLowerCase();if(N&&ke.getLanguage(N))try{return ke.highlight(h.code,{language:N,ignoreIllegals:!0}).value}catch{}return h.code.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;")});return()=>a("div",{class:"codeblock"},[a("div",{class:"codeblock-bar"},[a("span",{class:"codeblock-lang"},h.lang||""),a("button",{class:"codeblock-copy",onClick:T,title:"Copy code"},[s.value?a(lt,{class:"w-3 h-3"}):a(_t,{class:"w-3 h-3"}),s.value?"Copied":"Copy"])]),a("pre",{class:"codeblock-pre"},[a("code",{class:`hljs language-${(h.lang||"text").toLowerCase()}`,innerHTML:d.value})])])}}),ie=Pe({name:"TabbedCode",props:{tabs:{type:Array,required:!0},storageKey:{type:String,default:""}},setup(h){const s=(()=>{try{if(h.storageKey){const N=localStorage.getItem(h.storageKey);if(N&&h.tabs.some(ce=>ce.label===N))return N}}catch{}return h.tabs[0]?.label})(),T=Ae(s),d=N=>{T.value=N;try{h.storageKey&&localStorage.setItem(h.storageKey,N)}catch{}};return()=>{const N=h.tabs.find(ce=>ce.label===T.value)||h.tabs[0];return a("div",{class:"tabbed"},[a("div",{class:"tabbed-tabs"},h.tabs.map(ce=>a("button",{key:ce.label,class:["tabbed-tab",{active:ce.label===T.value}],onClick:()=>d(ce.label)},ce.label))),N.note?a("div",{class:"tabbed-note"},N.note):null,a(G,{code:N.code,lang:N.lang})])}}}),Fe=Pe({name:"Callout",props:{title:{type:String,default:""},icon:{type:[Object,Function],default:null}},setup(h,{slots:s}){return()=>a("div",{class:"callout"},[a("div",{class:"callout-head"},[h.icon?a(h.icon,{class:"callout-icon"}):null,h.title?a("span",null,h.title):null]),a("div",{class:"callout-body"},s.default?.())])}});return(h,s)=>{const T=Us("router-link");return U(),ge("div",oo,[t("header",no,[s[3]||(s[3]=t("div",{class:"docs-hero-bg","aria-hidden":"true"},null,-1)),t("div",ao,[t("div",ro,[s[1]||(s[1]=t("div",{class:"docs-hero-text"},[t("h1",{class:"docs-hero-title"}," Documentation "),t("p",{class:"docs-hero-sub"}," Everything you need to write, deploy, and operate functions on Orva. Handler contract, deploy + invoke, SDK, MCP, tracing, error taxonomy. ")],-1)),t("div",io,[t("button",{class:qe(["docs-hero-copy",{copied:te.value}]),"aria-label":te.value?"ChatGPT opened — paste docs in next message":"Open ChatGPT in a new tab with the docs auto-injected",onClick:He},[te.value?(U(),Ne(m(lt),{key:1,class:"w-3.5 h-3.5"})):(U(),Ne(m(qs),{key:0,class:"w-3.5 h-3.5"})),b(" "+I(te.value?"Opened — paste in chat":"Chat with ChatGPT"),1)],10,co),t("button",{class:qe(["docs-hero-copy",{copied:me.value}]),"aria-label":me.value?"Markdown copied to clipboard":"Copy entire docs page as Markdown",onClick:ve},[me.value?(U(),Ne(m(lt),{key:0,class:"w-3.5 h-3.5"})):(U(),Ne(m(_t),{key:1,class:"w-3.5 h-3.5"})),b(" "+I(me.value?"Copied as Markdown":"Copy as Markdown"),1)],10,lo)])]),t("nav",uo,[s[2]||(s[2]=t("span",{class:"docs-hero-toc-label"},"Jump to",-1)),(U(),ge(We,null,Xe(_,d=>t("a",{key:d.id,href:`#${d.id}`,class:qe(["docs-hero-toc-link",{active:X.value===d.id}])},[t("span",ho,I(d.num),1),t("span",null,I(d.label),1)],10,po)),64))])])]),t("section",go,[s[5]||(s[5]=t("div",{class:"doc-section-head"},[t("span",{class:"doc-section-num"},"01"),t("div",null,[t("h2",{class:"doc-section-title"}," Handler contract "),t("p",{class:"doc-lede"}," One exported function receives the inbound HTTP event and returns an HTTP-shaped response. The adapter handles serialization and headers. ")])],-1)),k(m(ie),{tabs:B.value,"storage-key":"docs.handler"},null,8,["tabs"]),s[6]||(s[6]=le('<div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Event shape </div><div class="doc-card-body"><code class="doc-chip">method</code><code class="doc-chip">path</code><code class="doc-chip">headers</code><code class="doc-chip">query</code><code class="doc-chip">body</code></div></div><div class="doc-card"><div class="doc-microlabel"> Response </div><div class="doc-card-body"><code class="doc-chip">{ statusCode, headers, body }</code><p class="mt-1.5 text-foreground-muted"> Non-string bodies are JSON-encoded by the adapter. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Runtime env </div><div class="doc-card-body"> Env vars and secrets land in <code class="doc-chip">process.env</code> / <code class="doc-chip">os.environ</code>. </div></div></div>',1)),t("div",bo,[t("table",fo,[s[4]||(s[4]=t("thead",null,[t("tr",null,[t("th",null,"Runtime"),t("th",null,"ID"),t("th",{class:"hidden sm:table-cell"}," Entrypoint "),t("th",{class:"hidden md:table-cell"}," Dependencies ")])],-1)),t("tbody",null,[(U(),ge(We,null,Xe(ne,d=>t("tr",{key:d.id},[t("td",mo,[(U(),Ne(Ht(d.icon),{class:"shrink-0"})),b(" "+I(d.name),1)]),t("td",vo,I(d.id),1),t("td",yo,I(d.entry),1),t("td",wo,I(d.deps),1)])),64))])])])]),t("section",ko,[s[11]||(s[11]=le('<div class="doc-section-head"><span class="doc-section-num">02</span><div><h2 class="doc-section-title"> Deploy &amp; invoke </h2><p class="doc-lede"> The dashboard handles day-to-day work; these calls are for CI and automation. Builds run async — poll <code class="doc-chip">/api/v1/deployments/&lt;id&gt;</code> or stream <code class="doc-chip">/api/v1/deployments/&lt;id&gt;/stream</code> until <code class="doc-chip">phase: done</code>. </p></div></div>',1)),k(m(L)),t("div",Eo,[t("div",_o,[s[7]||(s[7]=t("div",{class:"doc-step-label"},[t("span",{class:"doc-step-num"},"1"),b(" Create the function row ")],-1)),k(m(G),{code:Ee.value,lang:"bash"},null,8,["code"])]),t("div",So,[s[8]||(s[8]=t("div",{class:"doc-step-label"},[t("span",{class:"doc-step-num"},"2"),b(" Upload code ")],-1)),k(m(G),{code:_e.value,lang:"bash"},null,8,["code"])])]),t("div",To,[s[9]||(s[9]=t("div",{class:"doc-microlabel"}," Invoke ",-1)),k(m(ie),{tabs:ue.value,"storage-key":"docs.invoke"},null,8,["tabs"])]),k(m(Fe),{icon:m(St),title:"Custom routes"},{default:Me(()=>[...s[10]||(s[10]=[b(" Attach a friendly path with ",-1),t("code",{class:"doc-chip"},"POST /api/v1/routes",-1),b(". Reserved prefixes: ",-1),t("code",{class:"doc-chip"},"/api/",-1),t("code",{class:"doc-chip"},"/fn/",-1),t("code",{class:"doc-chip"},"/mcp/",-1),t("code",{class:"doc-chip"},"/web/",-1),t("code",{class:"doc-chip"},"/_orva/",-1),b(". ",-1)])]),_:1},8,["icon"])]),t("section",xo,[s[15]||(s[15]=t("div",{class:"doc-section-head"},[t("span",{class:"doc-section-num"},"03"),t("div",null,[t("h2",{class:"doc-section-title"}," Configuration reference "),t("p",{class:"doc-lede"}," Everything below lives on the function record. Secrets are stored encrypted and only decrypt into the worker environment at spawn time. ")])],-1)),t("div",Co,[t("table",Ao,[s[12]||(s[12]=t("thead",null,[t("tr",null,[t("th",null,"Field"),t("th",{class:"hidden sm:table-cell"}," Purpose "),t("th",null,"Behaviour")])],-1)),t("tbody",null,[(U(),ge(We,null,Xe(pe,d=>t("tr",{key:d.field,class:"align-top"},[t("td",Oo,[(U(),Ne(Ht(d.icon),{class:qe(["w-3.5 h-3.5 shrink-0",d.iconClass])},null,8,["class"])),t("code",null,I(d.field),1)]),t("td",Io,I(d.purpose),1),t("td",Ro,I(d.body),1)])),64))])])]),t("div",No,[s[13]||(s[13]=t("div",{class:"doc-microlabel"}," Set a secret ",-1)),k(m(G),{code:ae.value,lang:"bash"},null,8,["code"])]),t("details",Mo,[t("summary",Po,[k(m(Ut),{class:"w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted"}),s[14]||(s[14]=b(" Signed-invoke recipe (HMAC, opt-in) ",-1))]),t("div",Do,[k(m(G),{code:be.value,lang:"bash"},null,8,["code"])])])]),t("section",Lo,[s[21]||(s[21]=le('<div class="doc-section-head"><span class="doc-section-num">04</span><div><h2 class="doc-section-title"> SDK from inside a function </h2><p class="doc-lede"> The bundled <code class="doc-chip">orva</code> module exposes three primitives every function can use without extra dependencies: a per-function key/value store, in-process calls to other Orva functions, and a fire-and-forget background job queue. Routes through the per-process internal token injected at worker spawn time. </p></div></div><div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.kv</code></div><div class="doc-card-body"><code class="doc-chip">put / get / delete / list</code><p class="mt-1.5 text-foreground-muted"> Per-function namespace on SQLite, optional TTL. </p></div></div><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.invoke</code></div><div class="doc-card-body"><code class="doc-chip">invoke(name, payload)</code><p class="mt-1.5 text-foreground-muted"> In-process call to another function. 8-deep call cap. </p></div></div><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.jobs</code></div><div class="doc-card-body"><code class="doc-chip">jobs.enqueue(name, payload)</code><p class="mt-1.5 text-foreground-muted"> Fire-and-forget; persisted; retried with exp backoff. </p></div></div></div>',2)),t("div",Bo,[s[16]||(s[16]=t("div",{class:"doc-microlabel"}," KV — get/put with TTL ",-1)),k(m(ie),{tabs:Se,"storage-key":"docs.sdk.kv"}),s[17]||(s[17]=le('<p class="text-xs text-foreground-muted"> Browse / inspect / edit / delete / set keys without leaving the dashboard at <code class="doc-chip">/web/functions/&lt;name&gt;/kv</code> (or click the <code class="doc-chip">KV</code> button in the editor&#39;s action bar). REST mirror at <code class="doc-chip">GET/PUT/DELETE /api/v1/functions/&lt;id&gt;/kv[/&lt;key&gt;]</code>; MCP tools <code class="doc-chip">kv_list</code> / <code class="doc-chip">kv_get</code> / <code class="doc-chip">kv_put</code> / <code class="doc-chip">kv_delete</code> for agents. </p>',1))]),t("div",$o,[s[18]||(s[18]=t("div",{class:"doc-microlabel"}," Function-to-function — invoke() ",-1)),k(m(ie),{tabs:Le,"storage-key":"docs.sdk.invoke"})]),t("div",jo,[s[19]||(s[19]=t("div",{class:"doc-microlabel"}," Background jobs — jobs.enqueue() ",-1)),k(m(ie),{tabs:Be,"storage-key":"docs.sdk.jobs"})]),k(m(Fe),{icon:m(St),title:"Network mode"},{default:Me(()=>[...s[20]||(s[20]=[b(" The SDK reaches orvad over loopback through the host gateway, so the function needs ",-1),t("code",{class:"doc-chip"},'network_mode: "egress"',-1),b(". On the default ",-1),t("code",{class:"doc-chip"},'"none"',-1),b(" the SDK throws ",-1),t("code",{class:"doc-chip"},"OrvaUnavailableError",-1),b(" with a clear hint. ",-1)])]),_:1},8,["icon"])]),t("section",Ho,[t("div",Uo,[s[32]||(s[32]=t("span",{class:"doc-section-num"},"05",-1)),t("div",null,[s[31]||(s[31]=t("h2",{class:"doc-section-title"}," Schedules ",-1)),t("p",zo,[s[23]||(s[23]=b(" Fire any function on a cron expression. The scheduler runs as part of the orvad process — no external service. Manage from the ",-1)),k(T,{to:"/cron",class:"text-foreground hover:text-white underline decoration-dotted underline-offset-4"},{default:Me(()=>[...s[22]||(s[22]=[b("Schedules page",-1)])]),_:1}),s[24]||(s[24]=b(" or via the API. Standard 5-field cron with the usual shorthands (",-1)),s[25]||(s[25]=t("code",{class:"doc-chip"},"@daily",-1)),s[26]||(s[26]=b(", ",-1)),s[27]||(s[27]=t("code",{class:"doc-chip"},"@hourly",-1)),s[28]||(s[28]=b(", ",-1)),s[29]||(s[29]=t("code",{class:"doc-chip"},"*/5 * * * *",-1)),s[30]||(s[30]=b("). ",-1))])])]),k(m(ie),{tabs:De.value,"storage-key":"docs.cron"},null,8,["tabs"]),k(m(Fe),{icon:m(js),title:"Cron-fired headers"},{default:Me(()=>[...s[33]||(s[33]=[b(" Every cron-triggered invocation arrives at the function with ",-1),t("code",{class:"doc-chip"},"x-orva-trigger: cron",-1),b(" and ",-1),t("code",{class:"doc-chip"},"x-orva-cron-id: cron_…",-1),b(" on the event headers, so user code can branch on origin. ",-1)])]),_:1},8,["icon"])]),t("section",Ko,[t("div",Fo,[s[38]||(s[38]=t("span",{class:"doc-section-num"},"06",-1)),t("div",null,[s[37]||(s[37]=t("h2",{class:"doc-section-title"}," Webhooks ",-1)),t("p",Go,[s[35]||(s[35]=b(" Operator-managed subscriptions for system events. Configure URLs from the ",-1)),k(T,{to:"/webhooks",class:"text-foreground hover:text-white underline decoration-dotted underline-offset-4"},{default:Me(()=>[...s[34]||(s[34]=[b("Webhooks page",-1)])]),_:1}),s[36]||(s[36]=b("; Orva delivers signed POSTs to them when matching events fire (deployments, function lifecycle, cron failures, job outcomes). Subscriptions are global, not per-function. ",-1))])])]),k(m(J)),s[41]||(s[41]=le('<div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Headers </div><div class="doc-card-body"><code class="doc-chip">X-Orva-Event</code><code class="doc-chip">X-Orva-Delivery-Id</code><code class="doc-chip">X-Orva-Timestamp</code><code class="doc-chip">X-Orva-Signature</code></div></div><div class="doc-card"><div class="doc-microlabel"> Signature </div><div class="doc-card-body"><code class="doc-chip">sha256=hex(hmac(secret, ts.body))</code><p class="mt-1.5 text-foreground-muted"> Same shape as Stripe / signed-invoke. Receivers verify with the secret returned at create time. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Retries </div><div class="doc-card-body"><code class="doc-chip">5 attempts</code><code class="doc-chip">exp backoff (≤ 1h)</code><p class="mt-1.5 text-foreground-muted"> Receiver must 2xx within 15s. </p></div></div></div>',1)),t("div",qo,[t("table",Wo,[s[39]||(s[39]=t("thead",null,[t("tr",null,[t("th",null,"Event"),t("th",null,"When it fires")])],-1)),t("tbody",null,[(U(),ge(We,null,Xe($e,d=>t("tr",{key:d.name},[t("td",Xo,[t("code",null,I(d.name),1)]),t("td",Vo,I(d.when),1)])),64))])])]),t("div",Yo,[s[40]||(s[40]=t("div",{class:"doc-microlabel"}," Verify a delivery ",-1)),k(m(ie),{tabs:Ve,"storage-key":"docs.webhooks.verify"})])]),t("section",Zo,[s[51]||(s[51]=t("div",{class:"doc-section-head"},[t("span",{class:"doc-section-num"},"07"),t("div",null,[t("h2",{class:"doc-section-title"}," MCP — Model Context Protocol "),t("p",{class:"doc-lede"}," Same API surface the dashboard uses, exposed as 57 tools an agent can call directly. API key permissions scope the available tool set. ")])],-1)),t("div",Jo,[t("div",Qo,[s[42]||(s[42]=t("div",{class:"doc-microlabel"}," Endpoint ",-1)),t("div",en,[t("code",tn,I(f.value)+"/mcp",1)])]),s[43]||(s[43]=le('<div class="doc-card"><div class="doc-microlabel"> Auth header </div><div class="doc-card-body"><code class="doc-chip break-all">Authorization: Bearer &lt;token&gt;</code><p class="mt-1.5 text-foreground-muted"> Or as a fallback: <code class="doc-chip">X-Orva-API-Key: &lt;token&gt;</code></p></div></div><div class="doc-card"><div class="doc-microlabel"> Transport </div><div class="doc-card-body"><code class="doc-chip">Streamable HTTP</code><code class="doc-chip">MCP 2025-11-25</code></div></div>',2))]),k(m(Fe),{icon:m(tt),title:"Two header formats; same auth"},{default:Me(()=>[...s[44]||(s[44]=[b(" Either header works against the same API key store with identical permission gating. ",-1),t("code",{class:"doc-chip"},"Authorization: Bearer",-1),b(" is the MCP / OAuth 2 spec form — every MCP SDK (Claude Code, Claude Desktop, Cursor, mcp-remote, Python ",-1),t("code",{class:"doc-chip"},"mcp",-1),b(") configures it natively, so prefer it for new setups. ",-1),t("code",{class:"doc-chip"},"X-Orva-API-Key",-1),b(" is the same header the REST API accepts — useful when a tool reuses an existing Orva REST integration. Internally both paths SHA-256-hash the token and look it up against the same ",-1),t("code",{class:"doc-chip"},"api_keys",-1),b(" table. ",-1)])]),_:1},8,["icon"]),t("div",sn,[t("div",on,[k(m(tt),{class:"w-4 h-4 shrink-0 text-foreground-muted"}),Ue.value?(U(),ge("span",an,[s[47]||(s[47]=b(" Token minted: ",-1)),t("code",rn,I(dt.value)+"…",1),s[48]||(s[48]=b(" — shown once, copy now. ",-1))])):(U(),ge("span",nn,[s[45]||(s[45]=b(" Snippets show ",-1)),t("code",{class:"doc-chip"},I(ts)),s[46]||(s[46]=b(". Mint a token to substitute it everywhere. ",-1))]))]),t("button",{class:"doc-token-btn",disabled:ze.value,onClick:ut},[k(m(tt),{class:"w-3.5 h-3.5"}),b(" "+I(Ue.value?"Mint another":ze.value?"Minting…":"Generate token"),1)],8,cn)]),k(m(ie),{tabs:pt.value,"storage-key":"docs.mcp.install"},null,8,["tabs"]),t("details",ln,[t("summary",dn,[k(m(Ut),{class:"w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted"}),s[49]||(s[49]=b(" More clients (Cursor, VS Code, Codex CLI, OpenCode, Zed, Windsurf, ChatGPT, manual config) ",-1))]),t("div",un,[k(m(ie),{tabs:Ke.value,"storage-key":"docs.mcp.install.more"},null,8,["tabs"]),s[50]||(s[50]=t("div",{class:"doc-microlabel pt-1"}," Hand-edited config files ",-1)),k(m(ie),{tabs:st.value,"storage-key":"docs.mcp.manual"},null,8,["tabs"])])])]),t("section",pn,[s[52]||(s[52]=le('<div class="doc-section-head"><span class="doc-section-num">08</span><div><h2 class="doc-section-title"> System prompt for AI assistants </h2><p class="doc-lede"> Paste the prompt below into ChatGPT, Claude, Gemini, Cursor, Copilot, or any other AI tool to teach it Orva&#39;s full surface — handler contract, runtimes, sandbox limits, the in-sandbox <code class="doc-chip">orva</code> SDK (kv / invoke / jobs), cron triggers, system-event webhooks, auth modes, and production patterns. The model then turns &quot;describe what I want&quot; into a pasteable handler on the first try. </p></div></div>',1)),t("div",hn,[t("button",{class:qe(["ai-copy-btn",{copied:M.value}]),onClick:K},[M.value?(U(),Ne(m(lt),{key:0,class:"w-3.5 h-3.5"})):(U(),Ne(m(_t),{key:1,class:"w-3.5 h-3.5"})),b(" "+I(M.value?"Copied":"Copy system prompt"),1)],2)]),t("div",{class:qe(["prompt-collapse",{expanded:re.value}])},[k(m(G),{code:m(se),lang:"text"},null,8,["code"]),re.value?Hs("",!0):(U(),ge("div",gn))],2),t("button",{class:"prompt-expand-btn","aria-expanded":re.value,onClick:s[0]||(s[0]=d=>re.value=!re.value)},[k(m(Ns),{class:qe(["w-3.5 h-3.5 transition-transform",{"rotate-180":re.value}])},null,8,["class"]),b(" "+I(re.value?"Collapse system prompt":"Expand full system prompt (~400 lines)"),1)],8,bn)]),t("section",fn,[s[54]||(s[54]=le('<div class="doc-section-head"><span class="doc-section-num">09</span><div><h2 class="doc-section-title"> Tracing </h2><p class="doc-lede"> Every invocation chain is recorded as a causal trace — automatically, with <strong>zero changes to your function code</strong>. HTTP requests, F2F invokes, jobs, cron, inbound webhooks, and replays all stitch into the same tree. The dashboard renders it as a waterfall at <code class="doc-chip">/traces</code>. </p></div></div><p class="doc-prose"> Each execution row IS a span. Spans share a <code class="doc-chip">trace_id</code>; child spans point at their parent via <code class="doc-chip">parent_span_id</code>. You don&#39;t instantiate spans, you don&#39;t import a tracer — you just write your handler and the platform plumbs IDs through every internal hop. </p>',2)),k(m(de)),s[55]||(s[55]=t("h3",{class:"doc-h3"},"What user code sees",-1)),s[56]||(s[56]=t("p",{class:"doc-prose"}," Two env vars are stamped per invocation. Read them only if you want to log the trace_id alongside your own messages — they're optional. ",-1)),k(m(G),{code:Gt,lang:"text"}),s[57]||(s[57]=t("h3",{class:"doc-h3"},"Automatic propagation",-1)),s[58]||(s[58]=t("p",{class:"doc-prose"},[b(" When a function calls another via the SDK, the trace context flows through automatically. The called function becomes a child span of the caller; both share the same "),t("code",{class:"doc-chip"},"trace_id"),b(". ")],-1)),k(m(G),{code:qt,lang:"js"}),s[59]||(s[59]=le('<p class="doc-prose"> Job enqueues work the same way: <code class="doc-chip">orva.jobs.enqueue()</code> records the trace context on the job row. When the scheduler picks the job up later, the resulting execution lands in the same trace as the function that enqueued it — even if the gap is hours or days. </p><h3 class="doc-h3">Triggers</h3><p class="doc-prose"> Each span carries a <code class="doc-chip">trigger</code> label so the UI can show how the chain started. </p>',3)),t("div",mn,[t("table",vn,[s[53]||(s[53]=t("thead",null,[t("tr",null,[t("th",null,"Trigger"),t("th",null,"Meaning")])],-1)),t("tbody",null,[(U(),ge(We,null,Xe(Ye,d=>t("tr",{key:d.name},[t("td",yn,[t("code",null,I(d.name),1)]),t("td",wn,I(d.desc),1)])),64))])])]),s[60]||(s[60]=t("h3",{class:"doc-h3"},"External correlation (W3C traceparent)",-1)),s[61]||(s[61]=t("p",{class:"doc-prose"},[b(" Send a standard "),t("code",{class:"doc-chip"},"traceparent"),b(" header on the inbound HTTP request and Orva makes its trace a child of yours. The same trace_id is echoed back as "),t("code",{class:"doc-chip"},"X-Trace-Id"),b(" on every response, so external systems can correlate without parsing bodies. ")],-1)),k(m(G),{code:Wt,lang:"bash"}),s[62]||(s[62]=le('<h3 class="doc-h3">Outlier detection</h3><p class="doc-prose"> Each function maintains an in-memory rolling P95 baseline over its last 100 successful warm executions. An invocation is flagged as an outlier when it has at least 20 baseline samples AND its duration exceeds <strong>P95 × 2</strong>. Cold starts and errors are excluded from the baseline so a flapping function can&#39;t drag it down. The flag and baseline P95 are stored on the execution row and rendered as an amber flag icon next to the span. </p><h3 class="doc-h3">Where to look</h3><ul class="doc-list"><li><code class="doc-chip">/traces</code> — list of recent traces, filterable by function / status / outlier-only.</li><li><code class="doc-chip">/traces/:id</code> — waterfall + per-span detail. Click a span to jump to its execution in the Invocations log.</li><li><code class="doc-chip">GET /api/v1/traces/{id}</code> — full span tree as JSON. Pair with <code class="doc-chip">list_traces</code> / <code class="doc-chip">get_trace</code> MCP tools for AI agents.</li><li><code class="doc-chip">GET /api/v1/functions/{id}/baseline</code> — current P95/P99/mean for a function.</li></ul>',4))]),t("section",kn,[s[64]||(s[64]=le('<div class="doc-section-head"><span class="doc-section-num">10</span><div><h2 class="doc-section-title"> Errors &amp; recovery </h2><p class="doc-lede"> Every error response uses the same envelope so log scrapers and retries can match on <code class="doc-chip">code</code>. Deploys are content-addressed; rollback retargets the active version pointer and refreshes warm workers. </p></div></div>',1)),k(m(G),{code:Xt,lang:"json"}),t("div",En,[t("table",_n,[s[63]||(s[63]=t("thead",null,[t("tr",null,[t("th",null,"Code"),t("th",null,"When you see it")])],-1)),t("tbody",null,[(U(),ge(We,null,Xe(fe,d=>t("tr",{key:d.code},[t("td",Sn,[t("code",null,I(d.code),1)]),t("td",Tn,I(d.when),1)])),64))])])])]),t("section",xn,[s[81]||(s[81]=le('<div class="doc-section-head"><span class="doc-section-num">11</span><div><h2 class="doc-section-title"> CLI </h2><p class="doc-lede"><code class="doc-chip">orva</code> is a single static binary that talks to a remote (or local) Orva server over HTTPS. Same binary as the daemon — <code class="doc-chip">orva serve</code> starts a server, every other subcommand is a CLI client. Drop it on operator laptops, CI runners, or anywhere bash runs. </p></div></div><div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel">Install (server included)</div><div class="doc-card-body"><code class="doc-chip">curl … install.sh | sh</code><p class="mt-1.5 text-foreground-muted"> Full install: daemon + nsjail + rootfs + CLI. </p></div></div><div class="doc-card"><div class="doc-microlabel">Install (CLI only)</div><div class="doc-card-body"><code class="doc-chip">install.sh --cli-only</code><p class="mt-1.5 text-foreground-muted"> ~10 MB binary at <code>/usr/local/bin/orva</code>. No service. </p></div></div><div class="doc-card"><div class="doc-microlabel">Inside Docker</div><div class="doc-card-body"><code class="doc-chip">docker exec orva orva …</code><p class="mt-1.5 text-foreground-muted"> CLI auto-authed via <code>~/.orva/config.yaml</code>. </p></div></div></div><h3 class="doc-h3">Authenticate</h3>',3)),t("p",Cn,[s[66]||(s[66]=b(" The CLI reads ",-1)),s[67]||(s[67]=t("code",{class:"doc-chip"},"~/.orva/config.yaml",-1)),s[68]||(s[68]=b(" for ",-1)),s[69]||(s[69]=t("code",{class:"doc-chip"},"endpoint",-1)),s[70]||(s[70]=b(" + ",-1)),s[71]||(s[71]=t("code",{class:"doc-chip"},"api_key",-1)),s[72]||(s[72]=b(". Generate a key from ",-1)),k(T,{to:"/api-keys",class:"text-foreground hover:text-white underline decoration-dotted underline-offset-4"},{default:Me(()=>[...s[65]||(s[65]=[b("Keys",-1)])]),_:1}),s[73]||(s[73]=b(" in the dashboard, then: ",-1))]),k(m(G),{code:Vt,lang:"bash"}),s[82]||(s[82]=t("h3",{class:"doc-h3"},"Command index",-1)),t("div",An,[t("table",On,[s[74]||(s[74]=t("thead",null,[t("tr",null,[t("th",null,"Command"),t("th",null,"Subcommands"),t("th",{class:"hidden md:table-cell"},"Purpose")])],-1)),t("tbody",null,[(U(),ge(We,null,Xe(Ze,d=>t("tr",{key:d.cmd},[t("td",In,[t("code",null,"orva "+I(d.cmd),1)]),t("td",Rn,I(d.subs),1),t("td",Nn,I(d.purpose),1)])),64))])])]),s[83]||(s[83]=t("h3",{class:"doc-h3"},"Common recipes",-1)),t("div",Mn,[s[75]||(s[75]=t("div",{class:"doc-microlabel"},"Deploy a function from a directory",-1)),k(m(G),{code:Yt,lang:"bash"})]),t("div",Pn,[s[76]||(s[76]=t("div",{class:"doc-microlabel"},"Invoke + tail logs",-1)),k(m(G),{code:Zt,lang:"bash"})]),t("div",Dn,[s[77]||(s[77]=t("div",{class:"doc-microlabel"},"Manage KV state",-1)),k(m(G),{code:Jt,lang:"bash"})]),t("div",Ln,[s[78]||(s[78]=t("div",{class:"doc-microlabel"},"Secrets, cron, jobs, webhooks",-1)),k(m(G),{code:Qt,lang:"bash"})]),t("div",Bn,[s[79]||(s[79]=t("div",{class:"doc-microlabel"},"System health, metrics, vacuum",-1)),k(m(G),{code:es,lang:"bash"})]),k(m(Fe),{icon:m(tt),title:"Shell completion"},{default:Me(()=>[...s[80]||(s[80]=[b(" Generate completion for your shell: ",-1),t("code",{class:"doc-chip"},"orva completion bash | sudo tee /etc/bash_completion.d/orva",-1),b(", or ",-1),t("code",{class:"doc-chip"},"zsh",-1),b(" / ",-1),t("code",{class:"doc-chip"},"fish",-1),b(" / ",-1),t("code",{class:"doc-chip"},"powershell",-1),b(". Tab-completes commands, subcommands, and flags. ",-1)])]),_:1},8,["icon"])])])}}};export{Wn as default};
