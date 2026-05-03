import{C as ht,c as Pt}from"./clipboard-CWKUcUzk.js";import{a as An,b as Cn}from"./aiPrompts-DhPqzZpb.js";import{z as On,o as Rn,L as Nn,a as xe,b as t,q as st,m as Ke,f as _,k as m,t as D,F as Fe,n as ze,d as T,ae as ue,h as Ge,aQ as In,r as Le,p as Y,ap as Ve,y as k,Q as Mn,I as Pn,j as J,X as Bt,H as Bn}from"./index-C2YkBFhi.js";import{C as gt}from"./copy-p3wANccz.js";import{G as ft}from"./globe-C2p6Az_G.js";import{C as Dt}from"./chevron-right-bH4ALG3c.js";import{K as ot}from"./key-round-CSZ_XBJc.js";import{V as Dn}from"./variable-CTSet7vB.js";import{L as Ln}from"./lock-Dk5A-G5k.js";function $n(c){return c&&c.__esModule&&Object.prototype.hasOwnProperty.call(c,"default")?c.default:c}var bt,Lt;function Hn(){if(Lt)return bt;Lt=1;function c(e){return e instanceof Map?e.clear=e.delete=e.set=function(){throw new Error("map is read-only")}:e instanceof Set&&(e.add=e.clear=e.delete=function(){throw new Error("set is read-only")}),Object.freeze(e),Object.getOwnPropertyNames(e).forEach(s=>{const a=e[s],f=typeof a;(f==="object"||f==="function")&&!Object.isFrozen(a)&&c(a)}),e}class O{constructor(s){s.data===void 0&&(s.data={}),this.data=s.data,this.isMatchIgnored=!1}ignoreMatch(){this.isMatchIgnored=!0}}function p(e){return e.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;").replace(/'/g,"&#x27;")}function E(e,...s){const a=Object.create(null);for(const f in e)a[f]=e[f];return s.forEach(function(f){for(const P in f)a[P]=f[P]}),a}const W="</span>",Z=e=>!!e.scope,ee=(e,{prefix:s})=>{if(e.startsWith("language:"))return e.replace("language:","language-");if(e.includes(".")){const a=e.split(".");return[`${s}${a.shift()}`,...a.map((f,P)=>`${f}${"_".repeat(P+1)}`)].join(" ")}return`${s}${e}`};class R{constructor(s,a){this.buffer="",this.classPrefix=a.classPrefix,s.walk(this)}addText(s){this.buffer+=p(s)}openNode(s){if(!Z(s))return;const a=ee(s.scope,{prefix:this.classPrefix});this.span(a)}closeNode(s){Z(s)&&(this.buffer+=W)}value(){return this.buffer}span(s){this.buffer+=`<span class="${s}">`}}const K=(e={})=>{const s={children:[]};return Object.assign(s,e),s};class F{constructor(){this.rootNode=K(),this.stack=[this.rootNode]}get top(){return this.stack[this.stack.length-1]}get root(){return this.rootNode}add(s){this.top.children.push(s)}openNode(s){const a=K({scope:s});this.add(a),this.stack.push(a)}closeNode(){if(this.stack.length>1)return this.stack.pop()}closeAllNodes(){for(;this.closeNode(););}toJSON(){return JSON.stringify(this.rootNode,null,4)}walk(s){return this.constructor._walk(s,this.rootNode)}static _walk(s,a){return typeof a=="string"?s.addText(a):a.children&&(s.openNode(a),a.children.forEach(f=>this._walk(s,f)),s.closeNode(a)),s}static _collapse(s){typeof s!="string"&&s.children&&(s.children.every(a=>typeof a=="string")?s.children=[s.children.join("")]:s.children.forEach(a=>{F._collapse(a)}))}}class Q extends F{constructor(s){super(),this.options=s}addText(s){s!==""&&this.add(s)}startScope(s){this.openNode(s)}endScope(){this.closeNode()}__addSublanguage(s,a){const f=s.root;a&&(f.scope=`language:${a}`),this.add(f)}toHTML(){return new R(this,this.options).value()}finalize(){return this.closeAllNodes(),!0}}function z(e){return e?typeof e=="string"?e:e.source:null}function I(e){return L("(?=",e,")")}function ne(e){return L("(?:",e,")*")}function X(e){return L("(?:",e,")?")}function L(...e){return e.map(a=>z(a)).join("")}function ce(e){const s=e[e.length-1];return typeof s=="object"&&s.constructor===Object?(e.splice(e.length-1,1),s):{}}function se(...e){return"("+(ce(e).capture?"":"?:")+e.map(f=>z(f)).join("|")+")"}function le(e){return new RegExp(e.toString()+"|").exec("").length-1}function ye(e,s){const a=e&&e.exec(s);return a&&a.index===0}const Ee=/\[(?:[^\\\]]|\\.)*\]|\(\??|\\([1-9][0-9]*)|\\./;function oe(e,{joinWith:s}){let a=0;return e.map(f=>{a+=1;const P=a;let B=z(f),l="";for(;B.length>0;){const i=Ee.exec(B);if(!i){l+=B;break}l+=B.substring(0,i.index),B=B.substring(i.index+i[0].length),i[0][0]==="\\"&&i[1]?l+="\\"+String(Number(i[1])+P):(l+=i[0],i[0]==="("&&a++)}return l}).map(f=>`(${f})`).join(s)}const pe=/\b\B/,Re="[a-zA-Z]\\w*",_e="[a-zA-Z_]\\w*",Ne="\\b\\d+(\\.\\d+)?",Ie="(-?)(\\b0[xX][a-fA-F0-9]+|(\\b\\d+(\\.\\d*)?|\\.\\d+)([eE][-+]?\\d+)?)",Me="\\b(0b[01]+)",qe="!|!=|!==|%|%=|&|&&|&=|\\*|\\*=|\\+|\\+=|,|-|-=|/=|/|:|;|<<|<<=|<=|<|===|==|=|>>>=|>>=|>=|>>>|>>|>|\\?|\\[|\\{|\\(|\\^|\\^=|\\||\\|=|\\|\\||~",he=(e={})=>{const s=/^#![ ]*\//;return e.binary&&(e.begin=L(s,/.*\b/,e.binary,/\b.*/)),E({scope:"meta",begin:s,end:/$/,relevance:0,"on:begin":(a,f)=>{a.index!==0&&f.ignoreMatch()}},e)},ge={begin:"\\\\[\\s\\S]",relevance:0},We={scope:"string",begin:"'",end:"'",illegal:"\\n",contains:[ge]},fe={scope:"string",begin:'"',end:'"',illegal:"\\n",contains:[ge]},we={begin:/\b(a|an|the|are|I'm|isn't|don't|doesn't|won't|but|just|should|pretty|simply|enough|gonna|going|wtf|so|such|will|you|your|they|like|more)\b/},j=function(e,s,a={}){const f=E({scope:"comment",begin:e,end:s,contains:[]},a);f.contains.push({scope:"doctag",begin:"[ ]*(?=(TODO|FIXME|NOTE|BUG|OPTIMIZE|HACK|XXX):)",end:/(TODO|FIXME|NOTE|BUG|OPTIMIZE|HACK|XXX):/,excludeBegin:!0,relevance:0});const P=se("I","a","is","so","us","to","at","if","in","it","on",/[A-Za-z]+['](d|ve|re|ll|t|s|n)/,/[A-Za-z]+[-][a-z]+/,/[A-Za-z][a-z]{2,}/);return f.contains.push({begin:L(/[ ]+/,"(",P,/[.]?[:]?([.][ ]|[ ])/,"){3}")}),f},N=j("//","$"),ke=j("/\\*","\\*/"),Se=j("#","$"),Ae={scope:"number",begin:Ne,relevance:0},$e={scope:"number",begin:Ie,relevance:0},Ye={scope:"number",begin:Me,relevance:0},ae={scope:"regexp",begin:/\/(?=[^/\n]*\/)/,end:/\/[gimuy]*/,contains:[ge,{begin:/\[/,end:/\]/,relevance:0,contains:[ge]}]},re={scope:"title",begin:Re,relevance:0},He={scope:"title",begin:_e,relevance:0},h={begin:"\\.\\s*"+_e,relevance:0};var C=Object.freeze({__proto__:null,APOS_STRING_MODE:We,BACKSLASH_ESCAPE:ge,BINARY_NUMBER_MODE:Ye,BINARY_NUMBER_RE:Me,COMMENT:j,C_BLOCK_COMMENT_MODE:ke,C_LINE_COMMENT_MODE:N,C_NUMBER_MODE:$e,C_NUMBER_RE:Ie,END_SAME_AS_BEGIN:function(e){return Object.assign(e,{"on:begin":(s,a)=>{a.data._beginMatch=s[1]},"on:end":(s,a)=>{a.data._beginMatch!==s[1]&&a.ignoreMatch()}})},HASH_COMMENT_MODE:Se,IDENT_RE:Re,MATCH_NOTHING_RE:pe,METHOD_GUARD:h,NUMBER_MODE:Ae,NUMBER_RE:Ne,PHRASAL_WORDS_MODE:we,QUOTE_STRING_MODE:fe,REGEXP_MODE:ae,RE_STARTERS_RE:qe,SHEBANG:he,TITLE_MODE:re,UNDERSCORE_IDENT_RE:_e,UNDERSCORE_TITLE_MODE:He});function v(e,s){e.input[e.index-1]==="."&&s.ignoreMatch()}function $(e,s){e.className!==void 0&&(e.scope=e.className,delete e.className)}function ie(e,s){s&&e.beginKeywords&&(e.begin="\\b("+e.beginKeywords.split(" ").join("|")+")(?!\\.)(?=\\b|\\s)",e.__beforeBegin=v,e.keywords=e.keywords||e.beginKeywords,delete e.beginKeywords,e.relevance===void 0&&(e.relevance=0))}function M(e,s){Array.isArray(e.illegal)&&(e.illegal=se(...e.illegal))}function at(e,s){if(e.match){if(e.begin||e.end)throw new Error("begin & end are not supported with match");e.begin=e.match,delete e.match}}function Pe(e,s){e.relevance===void 0&&(e.relevance=1)}const Xt=(e,s)=>{if(!e.beforeMatch)return;if(e.starts)throw new Error("beforeMatch cannot be used with starts");const a=Object.assign({},e);Object.keys(e).forEach(f=>{delete e[f]}),e.keywords=a.keywords,e.begin=L(a.beforeMatch,I(a.begin)),e.starts={relevance:0,contains:[Object.assign(a,{endsParent:!0})]},e.relevance=0,delete a.beforeMatch},Vt=["of","and","for","in","not","or","if","then","parent","list","value"],Yt="keyword";function vt(e,s,a=Yt){const f=Object.create(null);return typeof e=="string"?P(a,e.split(" ")):Array.isArray(e)?P(a,e):Object.keys(e).forEach(function(B){Object.assign(f,vt(e[B],s,B))}),f;function P(B,l){s&&(l=l.map(i=>i.toLowerCase())),l.forEach(function(i){const g=i.split("|");f[g[0]]=[B,Zt(g[0],g[1])]})}}function Zt(e,s){return s?Number(s):Jt(e)?0:1}function Jt(e){return Vt.includes(e.toLowerCase())}const yt={},Be=e=>{console.error(e)},Et=(e,...s)=>{console.log(`WARN: ${e}`,...s)},je=(e,s)=>{yt[`${e}/${s}`]||(console.log(`Deprecated as of ${e}. ${s}`),yt[`${e}/${s}`]=!0)},Ze=new Error;function _t(e,s,{key:a}){let f=0;const P=e[a],B={},l={};for(let i=1;i<=s.length;i++)l[i+f]=P[i],B[i+f]=!0,f+=le(s[i-1]);e[a]=l,e[a]._emit=B,e[a]._multi=!0}function Qt(e){if(Array.isArray(e.begin)){if(e.skip||e.excludeBegin||e.returnBegin)throw Be("skip, excludeBegin, returnBegin not compatible with beginScope: {}"),Ze;if(typeof e.beginScope!="object"||e.beginScope===null)throw Be("beginScope must be object"),Ze;_t(e,e.begin,{key:"beginScope"}),e.begin=oe(e.begin,{joinWith:""})}}function en(e){if(Array.isArray(e.end)){if(e.skip||e.excludeEnd||e.returnEnd)throw Be("skip, excludeEnd, returnEnd not compatible with endScope: {}"),Ze;if(typeof e.endScope!="object"||e.endScope===null)throw Be("endScope must be object"),Ze;_t(e,e.end,{key:"endScope"}),e.end=oe(e.end,{joinWith:""})}}function tn(e){e.scope&&typeof e.scope=="object"&&e.scope!==null&&(e.beginScope=e.scope,delete e.scope)}function nn(e){tn(e),typeof e.beginScope=="string"&&(e.beginScope={_wrap:e.beginScope}),typeof e.endScope=="string"&&(e.endScope={_wrap:e.endScope}),Qt(e),en(e)}function sn(e){function s(l,i){return new RegExp(z(l),"m"+(e.case_insensitive?"i":"")+(e.unicodeRegex?"u":"")+(i?"g":""))}class a{constructor(){this.matchIndexes={},this.regexes=[],this.matchAt=1,this.position=0}addRule(i,g){g.position=this.position++,this.matchIndexes[this.matchAt]=g,this.regexes.push([g,i]),this.matchAt+=le(i)+1}compile(){this.regexes.length===0&&(this.exec=()=>null);const i=this.regexes.map(g=>g[1]);this.matcherRe=s(oe(i,{joinWith:"|"}),!0),this.lastIndex=0}exec(i){this.matcherRe.lastIndex=this.lastIndex;const g=this.matcherRe.exec(i);if(!g)return null;const G=g.findIndex((Xe,it)=>it>0&&Xe!==void 0),H=this.matchIndexes[G];return g.splice(0,G),Object.assign(g,H)}}class f{constructor(){this.rules=[],this.multiRegexes=[],this.count=0,this.lastIndex=0,this.regexIndex=0}getMatcher(i){if(this.multiRegexes[i])return this.multiRegexes[i];const g=new a;return this.rules.slice(i).forEach(([G,H])=>g.addRule(G,H)),g.compile(),this.multiRegexes[i]=g,g}resumingScanAtSamePosition(){return this.regexIndex!==0}considerAll(){this.regexIndex=0}addRule(i,g){this.rules.push([i,g]),g.type==="begin"&&this.count++}exec(i){const g=this.getMatcher(this.regexIndex);g.lastIndex=this.lastIndex;let G=g.exec(i);if(this.resumingScanAtSamePosition()&&!(G&&G.index===this.lastIndex)){const H=this.getMatcher(0);H.lastIndex=this.lastIndex+1,G=H.exec(i)}return G&&(this.regexIndex+=G.position+1,this.regexIndex===this.count&&this.considerAll()),G}}function P(l){const i=new f;return l.contains.forEach(g=>i.addRule(g.begin,{rule:g,type:"begin"})),l.terminatorEnd&&i.addRule(l.terminatorEnd,{type:"end"}),l.illegal&&i.addRule(l.illegal,{type:"illegal"}),i}function B(l,i){const g=l;if(l.isCompiled)return g;[$,at,nn,Xt].forEach(H=>H(l,i)),e.compilerExtensions.forEach(H=>H(l,i)),l.__beforeBegin=null,[ie,M,Pe].forEach(H=>H(l,i)),l.isCompiled=!0;let G=null;return typeof l.keywords=="object"&&l.keywords.$pattern&&(l.keywords=Object.assign({},l.keywords),G=l.keywords.$pattern,delete l.keywords.$pattern),G=G||/\w+/,l.keywords&&(l.keywords=vt(l.keywords,e.case_insensitive)),g.keywordPatternRe=s(G,!0),i&&(l.begin||(l.begin=/\B|\b/),g.beginRe=s(g.begin),!l.end&&!l.endsWithParent&&(l.end=/\B|\b/),l.end&&(g.endRe=s(g.end)),g.terminatorEnd=z(g.end)||"",l.endsWithParent&&i.terminatorEnd&&(g.terminatorEnd+=(l.end?"|":"")+i.terminatorEnd)),l.illegal&&(g.illegalRe=s(l.illegal)),l.contains||(l.contains=[]),l.contains=[].concat(...l.contains.map(function(H){return on(H==="self"?l:H)})),l.contains.forEach(function(H){B(H,g)}),l.starts&&B(l.starts,i),g.matcher=P(g),g}if(e.compilerExtensions||(e.compilerExtensions=[]),e.contains&&e.contains.includes("self"))throw new Error("ERR: contains `self` is not supported at the top-level of a language.  See documentation.");return e.classNameAliases=E(e.classNameAliases||{}),B(e)}function wt(e){return e?e.endsWithParent||wt(e.starts):!1}function on(e){return e.variants&&!e.cachedVariants&&(e.cachedVariants=e.variants.map(function(s){return E(e,{variants:null},s)})),e.cachedVariants?e.cachedVariants:wt(e)?E(e,{starts:e.starts?E(e.starts):null}):Object.isFrozen(e)?E(e):e}var an="11.11.1";class rn extends Error{constructor(s,a){super(s),this.name="HTMLInjectionError",this.html=a}}const rt=p,kt=E,St=Symbol("nomatch"),cn=7,Tt=function(e){const s=Object.create(null),a=Object.create(null),f=[];let P=!0;const B="Could not find the language '{}', did you forget to load/include a language module?",l={disableAutodetect:!0,name:"Plain text",contains:[]};let i={ignoreUnescapedHTML:!1,throwUnescapedHTML:!1,noHighlightRe:/^(no-?highlight)$/i,languageDetectRe:/\blang(?:uage)?-([\w-]+)\b/i,classPrefix:"hljs-",cssSelector:"pre code",languages:null,__emitter:Q};function g(o){return i.noHighlightRe.test(o)}function G(o){let u=o.className+" ";u+=o.parentNode?o.parentNode.className:"";const w=i.languageDetectRe.exec(u);if(w){const x=Ce(w[1]);return x||(Et(B.replace("{}",w[1])),Et("Falling back to no-highlight mode for this block.",o)),x?w[1]:"no-highlight"}return u.split(/\s+/).find(x=>g(x)||Ce(x))}function H(o,u,w){let x="",U="";typeof u=="object"?(x=o,w=u.ignoreIllegals,U=u.language):(je("10.7.0","highlight(lang, code, ...args) has been deprecated."),je("10.7.0",`Please use highlight(code, options) instead.
https://github.com/highlightjs/highlight.js/issues/2277`),U=o,x=u),w===void 0&&(w=!0);const de={code:x,language:U};Qe("before:highlight",de);const Oe=de.result?de.result:Xe(de.language,de.code,w);return Oe.code=de.code,Qe("after:highlight",Oe),Oe}function Xe(o,u,w,x){const U=Object.create(null);function de(r,d){return r.keywords[d]}function Oe(){if(!b.keywords){q.addText(A);return}let r=0;b.keywordPatternRe.lastIndex=0;let d=b.keywordPatternRe.exec(A),y="";for(;d;){y+=A.substring(r,d.index);const S=me.case_insensitive?d[0].toLowerCase():d[0],V=de(b,S);if(V){const[Te,Tn]=V;if(q.addText(y),y="",U[S]=(U[S]||0)+1,U[S]<=cn&&(nt+=Tn),Te.startsWith("_"))y+=d[0];else{const xn=me.classNameAliases[Te]||Te;be(d[0],xn)}}else y+=d[0];r=b.keywordPatternRe.lastIndex,d=b.keywordPatternRe.exec(A)}y+=A.substring(r),q.addText(y)}function et(){if(A==="")return;let r=null;if(typeof b.subLanguage=="string"){if(!s[b.subLanguage]){q.addText(A);return}r=Xe(b.subLanguage,A,!0,Mt[b.subLanguage]),Mt[b.subLanguage]=r._top}else r=ct(A,b.subLanguage.length?b.subLanguage:null);b.relevance>0&&(nt+=r.relevance),q.__addSublanguage(r._emitter,r.language)}function te(){b.subLanguage!=null?et():Oe(),A=""}function be(r,d){r!==""&&(q.startScope(d),q.addText(r),q.endScope())}function Ot(r,d){let y=1;const S=d.length-1;for(;y<=S;){if(!r._emit[y]){y++;continue}const V=me.classNameAliases[r[y]]||r[y],Te=d[y];V?be(Te,V):(A=Te,Oe(),A=""),y++}}function Rt(r,d){return r.scope&&typeof r.scope=="string"&&q.openNode(me.classNameAliases[r.scope]||r.scope),r.beginScope&&(r.beginScope._wrap?(be(A,me.classNameAliases[r.beginScope._wrap]||r.beginScope._wrap),A=""):r.beginScope._multi&&(Ot(r.beginScope,d),A="")),b=Object.create(r,{parent:{value:b}}),b}function Nt(r,d,y){let S=ye(r.endRe,y);if(S){if(r["on:end"]){const V=new O(r);r["on:end"](d,V),V.isMatchIgnored&&(S=!1)}if(S){for(;r.endsParent&&r.parent;)r=r.parent;return r}}if(r.endsWithParent)return Nt(r.parent,d,y)}function En(r){return b.matcher.regexIndex===0?(A+=r[0],1):(pt=!0,0)}function _n(r){const d=r[0],y=r.rule,S=new O(y),V=[y.__beforeBegin,y["on:begin"]];for(const Te of V)if(Te&&(Te(r,S),S.isMatchIgnored))return En(d);return y.skip?A+=d:(y.excludeBegin&&(A+=d),te(),!y.returnBegin&&!y.excludeBegin&&(A=d)),Rt(y,r),y.returnBegin?0:d.length}function wn(r){const d=r[0],y=u.substring(r.index),S=Nt(b,r,y);if(!S)return St;const V=b;b.endScope&&b.endScope._wrap?(te(),be(d,b.endScope._wrap)):b.endScope&&b.endScope._multi?(te(),Ot(b.endScope,r)):V.skip?A+=d:(V.returnEnd||V.excludeEnd||(A+=d),te(),V.excludeEnd&&(A=d));do b.scope&&q.closeNode(),!b.skip&&!b.subLanguage&&(nt+=b.relevance),b=b.parent;while(b!==S.parent);return S.starts&&Rt(S.starts,r),V.returnEnd?0:d.length}function kn(){const r=[];for(let d=b;d!==me;d=d.parent)d.scope&&r.unshift(d.scope);r.forEach(d=>q.openNode(d))}let tt={};function It(r,d){const y=d&&d[0];if(A+=r,y==null)return te(),0;if(tt.type==="begin"&&d.type==="end"&&tt.index===d.index&&y===""){if(A+=u.slice(d.index,d.index+1),!P){const S=new Error(`0 width match regex (${o})`);throw S.languageName=o,S.badRule=tt.rule,S}return 1}if(tt=d,d.type==="begin")return _n(d);if(d.type==="illegal"&&!w){const S=new Error('Illegal lexeme "'+y+'" for mode "'+(b.scope||"<unnamed>")+'"');throw S.mode=b,S}else if(d.type==="end"){const S=wn(d);if(S!==St)return S}if(d.type==="illegal"&&y==="")return A+=`
`,1;if(ut>1e5&&ut>d.index*3)throw new Error("potential infinite loop, way more iterations than matches");return A+=y,y.length}const me=Ce(o);if(!me)throw Be(B.replace("{}",o)),new Error('Unknown language: "'+o+'"');const Sn=sn(me);let dt="",b=x||Sn;const Mt={},q=new i.__emitter(i);kn();let A="",nt=0,De=0,ut=0,pt=!1;try{if(me.__emitTokens)me.__emitTokens(u,q);else{for(b.matcher.considerAll();;){ut++,pt?pt=!1:b.matcher.considerAll(),b.matcher.lastIndex=De;const r=b.matcher.exec(u);if(!r)break;const d=u.substring(De,r.index),y=It(d,r);De=r.index+y}It(u.substring(De))}return q.finalize(),dt=q.toHTML(),{language:o,value:dt,relevance:nt,illegal:!1,_emitter:q,_top:b}}catch(r){if(r.message&&r.message.includes("Illegal"))return{language:o,value:rt(u),illegal:!0,relevance:0,_illegalBy:{message:r.message,index:De,context:u.slice(De-100,De+100),mode:r.mode,resultSoFar:dt},_emitter:q};if(P)return{language:o,value:rt(u),illegal:!1,relevance:0,errorRaised:r,_emitter:q,_top:b};throw r}}function it(o){const u={value:rt(o),illegal:!1,relevance:0,_top:l,_emitter:new i.__emitter(i)};return u._emitter.addText(o),u}function ct(o,u){u=u||i.languages||Object.keys(s);const w=it(o),x=u.filter(Ce).filter(Ct).map(te=>Xe(te,o,!1));x.unshift(w);const U=x.sort((te,be)=>{if(te.relevance!==be.relevance)return be.relevance-te.relevance;if(te.language&&be.language){if(Ce(te.language).supersetOf===be.language)return 1;if(Ce(be.language).supersetOf===te.language)return-1}return 0}),[de,Oe]=U,et=de;return et.secondBest=Oe,et}function ln(o,u,w){const x=u&&a[u]||w;o.classList.add("hljs"),o.classList.add(`language-${x}`)}function lt(o){let u=null;const w=G(o);if(g(w))return;if(Qe("before:highlightElement",{el:o,language:w}),o.dataset.highlighted){console.log("Element previously highlighted. To highlight again, first unset `dataset.highlighted`.",o);return}if(o.children.length>0&&(i.ignoreUnescapedHTML||(console.warn("One of your code blocks includes unescaped HTML. This is a potentially serious security risk."),console.warn("https://github.com/highlightjs/highlight.js/wiki/security"),console.warn("The element with unescaped HTML:"),console.warn(o)),i.throwUnescapedHTML))throw new rn("One of your code blocks includes unescaped HTML.",o.innerHTML);u=o;const x=u.textContent,U=w?H(x,{language:w,ignoreIllegals:!0}):ct(x);o.innerHTML=U.value,o.dataset.highlighted="yes",ln(o,w,U.language),o.result={language:U.language,re:U.relevance,relevance:U.relevance},U.secondBest&&(o.secondBest={language:U.secondBest.language,relevance:U.secondBest.relevance}),Qe("after:highlightElement",{el:o,result:U,text:x})}function dn(o){i=kt(i,o)}const un=()=>{Je(),je("10.6.0","initHighlighting() deprecated.  Use highlightAll() now.")};function pn(){Je(),je("10.6.0","initHighlightingOnLoad() deprecated.  Use highlightAll() now.")}let xt=!1;function Je(){function o(){Je()}if(document.readyState==="loading"){xt||window.addEventListener("DOMContentLoaded",o,!1),xt=!0;return}document.querySelectorAll(i.cssSelector).forEach(lt)}function hn(o,u){let w=null;try{w=u(e)}catch(x){if(Be("Language definition for '{}' could not be registered.".replace("{}",o)),P)Be(x);else throw x;w=l}w.name||(w.name=o),s[o]=w,w.rawDefinition=u.bind(null,e),w.aliases&&At(w.aliases,{languageName:o})}function gn(o){delete s[o];for(const u of Object.keys(a))a[u]===o&&delete a[u]}function fn(){return Object.keys(s)}function Ce(o){return o=(o||"").toLowerCase(),s[o]||s[a[o]]}function At(o,{languageName:u}){typeof o=="string"&&(o=[o]),o.forEach(w=>{a[w.toLowerCase()]=u})}function Ct(o){const u=Ce(o);return u&&!u.disableAutodetect}function bn(o){o["before:highlightBlock"]&&!o["before:highlightElement"]&&(o["before:highlightElement"]=u=>{o["before:highlightBlock"](Object.assign({block:u.el},u))}),o["after:highlightBlock"]&&!o["after:highlightElement"]&&(o["after:highlightElement"]=u=>{o["after:highlightBlock"](Object.assign({block:u.el},u))})}function mn(o){bn(o),f.push(o)}function vn(o){const u=f.indexOf(o);u!==-1&&f.splice(u,1)}function Qe(o,u){const w=o;f.forEach(function(x){x[w]&&x[w](u)})}function yn(o){return je("10.7.0","highlightBlock will be removed entirely in v12.0"),je("10.7.0","Please use highlightElement now."),lt(o)}Object.assign(e,{highlight:H,highlightAuto:ct,highlightAll:Je,highlightElement:lt,highlightBlock:yn,configure:dn,initHighlighting:un,initHighlightingOnLoad:pn,registerLanguage:hn,unregisterLanguage:gn,listLanguages:fn,getLanguage:Ce,registerAliases:At,autoDetection:Ct,inherit:kt,addPlugin:mn,removePlugin:vn}),e.debugMode=function(){P=!1},e.safeMode=function(){P=!0},e.versionString=an,e.regex={concat:L,lookahead:I,either:se,optional:X,anyNumberOfTimes:ne};for(const o in C)typeof C[o]=="object"&&c(C[o]);return Object.assign(e,C),e},Ue=Tt({});return Ue.newInstance=()=>Tt({}),bt=Ue,Ue.HighlightJS=Ue,Ue.default=Ue,bt}var jn=Hn();const ve=$n(jn);function Un(c){const O=c.regex,p=new RegExp("[\\p{XID_Start}_]\\p{XID_Continue}*","u"),E=["and","as","assert","async","await","break","case","class","continue","def","del","elif","else","except","finally","for","from","global","if","import","in","is","lambda","match","nonlocal|10","not","or","pass","raise","return","try","while","with","yield"],R={$pattern:/[A-Za-z]\w+|__\w+__/,keyword:E,built_in:["__import__","abs","all","any","ascii","bin","bool","breakpoint","bytearray","bytes","callable","chr","classmethod","compile","complex","delattr","dict","dir","divmod","enumerate","eval","exec","filter","float","format","frozenset","getattr","globals","hasattr","hash","help","hex","id","input","int","isinstance","issubclass","iter","len","list","locals","map","max","memoryview","min","next","object","oct","open","ord","pow","print","property","range","repr","reversed","round","set","setattr","slice","sorted","staticmethod","str","sum","super","tuple","type","vars","zip"],literal:["__debug__","Ellipsis","False","None","NotImplemented","True"],type:["Any","Callable","Coroutine","Dict","List","Literal","Generic","Optional","Sequence","Set","Tuple","Type","Union"]},K={className:"meta",begin:/^(>>>|\.\.\.) /},F={className:"subst",begin:/\{/,end:/\}/,keywords:R,illegal:/#/},Q={begin:/\{\{/,relevance:0},z={className:"string",contains:[c.BACKSLASH_ESCAPE],variants:[{begin:/([uU]|[bB]|[rR]|[bB][rR]|[rR][bB])?'''/,end:/'''/,contains:[c.BACKSLASH_ESCAPE,K],relevance:10},{begin:/([uU]|[bB]|[rR]|[bB][rR]|[rR][bB])?"""/,end:/"""/,contains:[c.BACKSLASH_ESCAPE,K],relevance:10},{begin:/([fF][rR]|[rR][fF]|[fF])'''/,end:/'''/,contains:[c.BACKSLASH_ESCAPE,K,Q,F]},{begin:/([fF][rR]|[rR][fF]|[fF])"""/,end:/"""/,contains:[c.BACKSLASH_ESCAPE,K,Q,F]},{begin:/([uU]|[rR])'/,end:/'/,relevance:10},{begin:/([uU]|[rR])"/,end:/"/,relevance:10},{begin:/([bB]|[bB][rR]|[rR][bB])'/,end:/'/},{begin:/([bB]|[bB][rR]|[rR][bB])"/,end:/"/},{begin:/([fF][rR]|[rR][fF]|[fF])'/,end:/'/,contains:[c.BACKSLASH_ESCAPE,Q,F]},{begin:/([fF][rR]|[rR][fF]|[fF])"/,end:/"/,contains:[c.BACKSLASH_ESCAPE,Q,F]},c.APOS_STRING_MODE,c.QUOTE_STRING_MODE]},I="[0-9](_?[0-9])*",ne=`(\\b(${I}))?\\.(${I})|\\b(${I})\\.`,X=`\\b|${E.join("|")}`,L={className:"number",relevance:0,variants:[{begin:`(\\b(${I})|(${ne}))[eE][+-]?(${I})[jJ]?(?=${X})`},{begin:`(${ne})[jJ]?`},{begin:`\\b([1-9](_?[0-9])*|0+(_?0)*)[lLjJ]?(?=${X})`},{begin:`\\b0[bB](_?[01])+[lL]?(?=${X})`},{begin:`\\b0[oO](_?[0-7])+[lL]?(?=${X})`},{begin:`\\b0[xX](_?[0-9a-fA-F])+[lL]?(?=${X})`},{begin:`\\b(${I})[jJ](?=${X})`}]},ce={className:"comment",begin:O.lookahead(/# type:/),end:/$/,keywords:R,contains:[{begin:/# type:/},{begin:/#/,end:/\b\B/,endsWithParent:!0}]},se={className:"params",variants:[{className:"",begin:/\(\s*\)/,skip:!0},{begin:/\(/,end:/\)/,excludeBegin:!0,excludeEnd:!0,keywords:R,contains:["self",K,L,z,c.HASH_COMMENT_MODE]}]};return F.contains=[z,L,K],{name:"Python",aliases:["py","gyp","ipython"],unicodeRegex:!0,keywords:R,illegal:/(<\/|\?)|=>/,contains:[K,L,{scope:"variable.language",match:/\bself\b/},{beginKeywords:"if",relevance:0},{match:/\bor\b/,scope:"keyword"},z,ce,c.HASH_COMMENT_MODE,{match:[/\bdef/,/\s+/,p],scope:{1:"keyword",3:"title.function"},contains:[se]},{variants:[{match:[/\bclass/,/\s+/,p,/\s*/,/\(\s*/,p,/\s*\)/]},{match:[/\bclass/,/\s+/,p]}],scope:{1:"keyword",3:"title.class",6:"title.class.inherited"}},{className:"meta",begin:/^[\t ]*@/,end:/(?=#)|$/,contains:[L,se,z]}]}}const $t="[A-Za-z$_][0-9A-Za-z$_]*",Kn=["as","in","of","if","for","while","finally","var","new","function","do","return","void","else","break","catch","instanceof","with","throw","case","default","try","switch","continue","typeof","delete","let","yield","const","class","debugger","async","await","static","import","from","export","extends","using"],Fn=["true","false","null","undefined","NaN","Infinity"],Gt=["Object","Function","Boolean","Symbol","Math","Date","Number","BigInt","String","RegExp","Array","Float32Array","Float64Array","Int8Array","Uint8Array","Uint8ClampedArray","Int16Array","Int32Array","Uint16Array","Uint32Array","BigInt64Array","BigUint64Array","Set","Map","WeakSet","WeakMap","ArrayBuffer","SharedArrayBuffer","Atomics","DataView","JSON","Promise","Generator","GeneratorFunction","AsyncFunction","Reflect","Proxy","Intl","WebAssembly"],qt=["Error","EvalError","InternalError","RangeError","ReferenceError","SyntaxError","TypeError","URIError"],Wt=["setInterval","setTimeout","clearInterval","clearTimeout","require","exports","eval","isFinite","isNaN","parseFloat","parseInt","decodeURI","decodeURIComponent","encodeURI","encodeURIComponent","escape","unescape"],zn=["arguments","this","super","console","window","document","localStorage","sessionStorage","module","global"],Gn=[].concat(Wt,Gt,qt);function Ht(c){const O=c.regex,p=(j,{after:N})=>{const ke="</"+j[0].slice(1);return j.input.indexOf(ke,N)!==-1},E=$t,W={begin:"<>",end:"</>"},Z=/<[A-Za-z0-9\\._:-]+\s*\/>/,ee={begin:/<[A-Za-z0-9\\._:-]+/,end:/\/[A-Za-z0-9\\._:-]+>|\/>/,isTrulyOpeningTag:(j,N)=>{const ke=j[0].length+j.index,Se=j.input[ke];if(Se==="<"||Se===","){N.ignoreMatch();return}Se===">"&&(p(j,{after:ke})||N.ignoreMatch());let Ae;const $e=j.input.substring(ke);if(Ae=$e.match(/^\s*=/)){N.ignoreMatch();return}if((Ae=$e.match(/^\s+extends\s+/))&&Ae.index===0){N.ignoreMatch();return}}},R={$pattern:$t,keyword:Kn,literal:Fn,built_in:Gn,"variable.language":zn},K="[0-9](_?[0-9])*",F=`\\.(${K})`,Q="0|[1-9](_?[0-9])*|0[0-7]*[89][0-9]*",z={className:"number",variants:[{begin:`(\\b(${Q})((${F})|\\.)?|(${F}))[eE][+-]?(${K})\\b`},{begin:`\\b(${Q})\\b((${F})\\b|\\.)?|(${F})\\b`},{begin:"\\b(0|[1-9](_?[0-9])*)n\\b"},{begin:"\\b0[xX][0-9a-fA-F](_?[0-9a-fA-F])*n?\\b"},{begin:"\\b0[bB][0-1](_?[0-1])*n?\\b"},{begin:"\\b0[oO][0-7](_?[0-7])*n?\\b"},{begin:"\\b0[0-7]+n?\\b"}],relevance:0},I={className:"subst",begin:"\\$\\{",end:"\\}",keywords:R,contains:[]},ne={begin:".?html`",end:"",starts:{end:"`",returnEnd:!1,contains:[c.BACKSLASH_ESCAPE,I],subLanguage:"xml"}},X={begin:".?css`",end:"",starts:{end:"`",returnEnd:!1,contains:[c.BACKSLASH_ESCAPE,I],subLanguage:"css"}},L={begin:".?gql`",end:"",starts:{end:"`",returnEnd:!1,contains:[c.BACKSLASH_ESCAPE,I],subLanguage:"graphql"}},ce={className:"string",begin:"`",end:"`",contains:[c.BACKSLASH_ESCAPE,I]},le={className:"comment",variants:[c.COMMENT(/\/\*\*(?!\/)/,"\\*/",{relevance:0,contains:[{begin:"(?=@[A-Za-z]+)",relevance:0,contains:[{className:"doctag",begin:"@[A-Za-z]+"},{className:"type",begin:"\\{",end:"\\}",excludeEnd:!0,excludeBegin:!0,relevance:0},{className:"variable",begin:E+"(?=\\s*(-)|$)",endsParent:!0,relevance:0},{begin:/(?=[^\n])\s/,relevance:0}]}]}),c.C_BLOCK_COMMENT_MODE,c.C_LINE_COMMENT_MODE]},ye=[c.APOS_STRING_MODE,c.QUOTE_STRING_MODE,ne,X,L,ce,{match:/\$\d+/},z];I.contains=ye.concat({begin:/\{/,end:/\}/,keywords:R,contains:["self"].concat(ye)});const Ee=[].concat(le,I.contains),oe=Ee.concat([{begin:/(\s*)\(/,end:/\)/,keywords:R,contains:["self"].concat(Ee)}]),pe={className:"params",begin:/(\s*)\(/,end:/\)/,excludeBegin:!0,excludeEnd:!0,keywords:R,contains:oe},Re={variants:[{match:[/class/,/\s+/,E,/\s+/,/extends/,/\s+/,O.concat(E,"(",O.concat(/\./,E),")*")],scope:{1:"keyword",3:"title.class",5:"keyword",7:"title.class.inherited"}},{match:[/class/,/\s+/,E],scope:{1:"keyword",3:"title.class"}}]},_e={relevance:0,match:O.either(/\bJSON/,/\b[A-Z][a-z]+([A-Z][a-z]*|\d)*/,/\b[A-Z]{2,}([A-Z][a-z]+|\d)+([A-Z][a-z]*)*/,/\b[A-Z]{2,}[a-z]+([A-Z][a-z]+|\d)*([A-Z][a-z]*)*/),className:"title.class",keywords:{_:[...Gt,...qt]}},Ne={label:"use_strict",className:"meta",relevance:10,begin:/^\s*['"]use (strict|asm)['"]/},Ie={variants:[{match:[/function/,/\s+/,E,/(?=\s*\()/]},{match:[/function/,/\s*(?=\()/]}],className:{1:"keyword",3:"title.function"},label:"func.def",contains:[pe],illegal:/%/},Me={relevance:0,match:/\b[A-Z][A-Z_0-9]+\b/,className:"variable.constant"};function qe(j){return O.concat("(?!",j.join("|"),")")}const he={match:O.concat(/\b/,qe([...Wt,"super","import"].map(j=>`${j}\\s*\\(`)),E,O.lookahead(/\s*\(/)),className:"title.function",relevance:0},ge={begin:O.concat(/\./,O.lookahead(O.concat(E,/(?![0-9A-Za-z$_(])/))),end:E,excludeBegin:!0,keywords:"prototype",className:"property",relevance:0},We={match:[/get|set/,/\s+/,E,/(?=\()/],className:{1:"keyword",3:"title.function"},contains:[{begin:/\(\)/},pe]},fe="(\\([^()]*(\\([^()]*(\\([^()]*\\)[^()]*)*\\)[^()]*)*\\)|"+c.UNDERSCORE_IDENT_RE+")\\s*=>",we={match:[/const|var|let/,/\s+/,E,/\s*/,/=\s*/,/(async\s*)?/,O.lookahead(fe)],keywords:"async",className:{1:"keyword",3:"title.function"},contains:[pe]};return{name:"JavaScript",aliases:["js","jsx","mjs","cjs"],keywords:R,exports:{PARAMS_CONTAINS:oe,CLASS_REFERENCE:_e},illegal:/#(?![$_A-z])/,contains:[c.SHEBANG({label:"shebang",binary:"node",relevance:5}),Ne,c.APOS_STRING_MODE,c.QUOTE_STRING_MODE,ne,X,L,ce,le,{match:/\$\d+/},z,_e,{scope:"attr",match:E+O.lookahead(":"),relevance:0},we,{begin:"("+c.RE_STARTERS_RE+"|\\b(case|return|throw)\\b)\\s*",keywords:"return throw case",relevance:0,contains:[le,c.REGEXP_MODE,{className:"function",begin:fe,returnBegin:!0,end:"\\s*=>",contains:[{className:"params",variants:[{begin:c.UNDERSCORE_IDENT_RE,relevance:0},{className:null,begin:/\(\s*\)/,skip:!0},{begin:/(\s*)\(/,end:/\)/,excludeBegin:!0,excludeEnd:!0,keywords:R,contains:oe}]}]},{begin:/,/,relevance:0},{match:/\s+/,relevance:0},{variants:[{begin:W.begin,end:W.end},{match:Z},{begin:ee.begin,"on:begin":ee.isTrulyOpeningTag,end:ee.end}],subLanguage:"xml",contains:[{begin:ee.begin,end:ee.end,skip:!0,contains:["self"]}]}]},Ie,{beginKeywords:"while if switch catch for"},{begin:"\\b(?!function)"+c.UNDERSCORE_IDENT_RE+"\\([^()]*(\\([^()]*(\\([^()]*\\)[^()]*)*\\)[^()]*)*\\)\\s*\\{",returnBegin:!0,label:"func.def",contains:[pe,c.inherit(c.TITLE_MODE,{begin:E,className:"title.function"})]},{match:/\.\.\./,relevance:0},ge,{match:"\\$"+E,relevance:0},{match:[/\bconstructor(?=\s*\()/],className:{1:"title.function"},contains:[pe]},he,Me,Re,We,{match:/\$[(.]/}]}}function qn(c){const O={className:"attr",begin:/"(\\.|[^\\"\r\n])*"(?=\s*:)/,relevance:1.01},p={match:/[{}[\],:]/,className:"punctuation",relevance:0},E=["true","false","null"],W={scope:"literal",beginKeywords:E.join(" ")};return{name:"JSON",aliases:["jsonc"],keywords:{literal:E},contains:[O,p,c.QUOTE_STRING_MODE,W,c.C_NUMBER_MODE,c.C_LINE_COMMENT_MODE,c.C_BLOCK_COMMENT_MODE],illegal:"\\S"}}function mt(c){const O=c.regex,p={},E={begin:/\$\{/,end:/\}/,contains:["self",{begin:/:-/,contains:[p]}]};Object.assign(p,{className:"variable",variants:[{begin:O.concat(/\$[\w\d#@][\w\d_]*/,"(?![\\w\\d])(?![$])")},E]});const W={className:"subst",begin:/\$\(/,end:/\)/,contains:[c.BACKSLASH_ESCAPE]},Z=c.inherit(c.COMMENT(),{match:[/(^|\s)/,/#.*$/],scope:{2:"comment"}}),ee={begin:/<<-?\s*(?=\w+)/,starts:{contains:[c.END_SAME_AS_BEGIN({begin:/(\w+)/,end:/(\w+)/,className:"string"})]}},R={className:"string",begin:/"/,end:/"/,contains:[c.BACKSLASH_ESCAPE,p,W]};W.contains.push(R);const K={match:/\\"/},F={className:"string",begin:/'/,end:/'/},Q={match:/\\'/},z={begin:/\$?\(\(/,end:/\)\)/,contains:[{begin:/\d+#[0-9a-f]+/,className:"number"},c.NUMBER_MODE,p]},I=["fish","bash","zsh","sh","csh","ksh","tcsh","dash","scsh"],ne=c.SHEBANG({binary:`(${I.join("|")})`,relevance:10}),X={className:"function",begin:/\w[\w\d_]*\s*\(\s*\)\s*\{/,returnBegin:!0,contains:[c.inherit(c.TITLE_MODE,{begin:/\w[\w\d_]*/})],relevance:0},L=["if","then","else","elif","fi","time","for","while","until","in","do","done","case","esac","coproc","function","select"],ce=["true","false"],se={match:/(\/[a-z._-]+)+/},le=["break","cd","continue","eval","exec","exit","export","getopts","hash","pwd","readonly","return","shift","test","times","trap","umask","unset"],ye=["alias","bind","builtin","caller","command","declare","echo","enable","help","let","local","logout","mapfile","printf","read","readarray","source","sudo","type","typeset","ulimit","unalias"],Ee=["autoload","bg","bindkey","bye","cap","chdir","clone","comparguments","compcall","compctl","compdescribe","compfiles","compgroups","compquote","comptags","comptry","compvalues","dirs","disable","disown","echotc","echoti","emulate","fc","fg","float","functions","getcap","getln","history","integer","jobs","kill","limit","log","noglob","popd","print","pushd","pushln","rehash","sched","setcap","setopt","stat","suspend","ttyctl","unfunction","unhash","unlimit","unsetopt","vared","wait","whence","where","which","zcompile","zformat","zftp","zle","zmodload","zparseopts","zprof","zpty","zregexparse","zsocket","zstyle","ztcp"],oe=["chcon","chgrp","chown","chmod","cp","dd","df","dir","dircolors","ln","ls","mkdir","mkfifo","mknod","mktemp","mv","realpath","rm","rmdir","shred","sync","touch","truncate","vdir","b2sum","base32","base64","cat","cksum","comm","csplit","cut","expand","fmt","fold","head","join","md5sum","nl","numfmt","od","paste","ptx","pr","sha1sum","sha224sum","sha256sum","sha384sum","sha512sum","shuf","sort","split","sum","tac","tail","tr","tsort","unexpand","uniq","wc","arch","basename","chroot","date","dirname","du","echo","env","expr","factor","groups","hostid","id","link","logname","nice","nohup","nproc","pathchk","pinky","printenv","printf","pwd","readlink","runcon","seq","sleep","stat","stdbuf","stty","tee","test","timeout","tty","uname","unlink","uptime","users","who","whoami","yes"];return{name:"Bash",aliases:["sh","zsh"],keywords:{$pattern:/\b[a-z][a-z0-9._-]+\b/,keyword:L,literal:ce,built_in:[...le,...ye,"set","shopt",...Ee,...oe]},contains:[ne,c.SHEBANG(),X,z,Z,ee,se,R,K,F,Q,p]}}function Wn(c){const O=c.regex,p="HTTP/([32]|1\\.[01])",E=/[A-Za-z][A-Za-z0-9-]*/,W={className:"attribute",begin:O.concat("^",E,"(?=\\:\\s)"),starts:{contains:[{className:"punctuation",begin:/: /,relevance:0,starts:{end:"$",relevance:0}}]}},Z=[W,{begin:"\\n\\n",starts:{subLanguage:[],endsWithParent:!0}}];return{name:"HTTP",aliases:["https"],illegal:/\S/,contains:[{begin:"^(?="+p+" \\d{3})",end:/$/,contains:[{className:"meta",begin:p},{className:"number",begin:"\\b\\d{3}\\b"}],starts:{end:/\b\B/,illegal:/\S/,contains:Z}},{begin:"(?=^[A-Z]+ (.*?) "+p+"$)",end:/$/,contains:[{className:"string",begin:" ",end:" ",excludeBegin:!0,excludeEnd:!0},{className:"meta",begin:p},{className:"keyword",begin:"[A-Z]+"}],starts:{end:/\b\B/,illegal:/\S/,contains:Z}},c.inherit(W,{relevance:0})]}}const Xn={class:"space-y-12 pb-16"},Vn={class:"docs-hero"},Yn={class:"docs-hero-content"},Zn={class:"docs-hero-row"},Jn={class:"docs-hero-actions"},Qn=["aria-label"],es={class:"docs-hero-toc","aria-label":"Jump to docs section"},ts=["href"],ns={class:"docs-hero-toc-num"},ss={id:"handler",class:"space-y-5 scroll-mt-6"},os={class:"doc-table-wrap"},as={class:"doc-table"},rs={class:"doc-cell-key"},is={class:"doc-cell-mono"},cs={class:"doc-cell-mono hidden sm:table-cell"},ls={class:"doc-cell-mono hidden md:table-cell"},ds={id:"deploy",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},us={class:"grid grid-cols-1 lg:grid-cols-2 gap-3"},ps={class:"space-y-2"},hs={class:"space-y-2"},gs={class:"space-y-2"},fs={id:"config",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},bs={class:"doc-table-wrap"},ms={class:"doc-table"},vs={class:"doc-cell-key whitespace-nowrap"},ys={class:"doc-cell-mono hidden sm:table-cell whitespace-nowrap"},Es={class:"doc-cell-body"},_s={class:"space-y-2"},ws={class:"doc-details group"},ks={class:"doc-details-summary"},Ss={class:"doc-details-body"},Ts={id:"sdk",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},xs={class:"space-y-2"},As={class:"space-y-2"},Cs={class:"space-y-2"},Os={id:"schedules",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Rs={class:"doc-section-head"},Ns={class:"doc-lede"},Is={id:"webhooks",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Ms={class:"doc-section-head"},Ps={class:"doc-lede"},Bs={class:"doc-table-wrap"},Ds={class:"doc-table"},Ls={class:"doc-cell-key whitespace-nowrap"},$s={class:"doc-cell-body"},Hs={class:"space-y-2"},js={id:"mcp",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},Us={class:"grid grid-cols-1 md:grid-cols-3 gap-3"},Ks={class:"doc-card"},Fs={class:"doc-card-body"},zs={class:"doc-chip break-all"},Gs={class:"doc-token-bar"},qs={class:"flex items-center gap-2 min-w-0 flex-1"},Ws={key:0,class:"text-sm text-foreground-muted truncate"},Xs={key:1,class:"text-sm text-success truncate"},Vs={class:"doc-chip"},Ys=["disabled"],Zs={class:"doc-details group"},Js={class:"doc-details-summary"},Qs={class:"doc-details-body space-y-4"},eo={id:"generate",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},to={class:"ai-prompt-actions"},no={id:"tracing",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},so={class:"doc-table-wrap"},oo={class:"doc-table"},ao={class:"doc-cell-key whitespace-nowrap"},ro={class:"doc-cell-body"},io={id:"errors",class:"space-y-5 scroll-mt-6 border-t border-border pt-12"},co={class:"doc-table-wrap"},lo={class:"doc-table"},uo={class:"doc-cell-key whitespace-nowrap"},po={class:"doc-cell-body"},jt=`# Available inside every running function — refresh per-invocation:
ORVA_TRACE_ID=tr_3e39f6991c66f140577c6021da7dd13b   # one per causal chain
ORVA_SPAN_ID=sp_4ceba57f6b1c982e                    # this execution

# Python:        os.environ["ORVA_TRACE_ID"]
# Node.js:       process.env.ORVA_TRACE_ID
# Reading them is optional — the platform records the trace for you.`,Ut=`// Function A — calls B via the SDK. Trace context flows automatically.
const { invoke, jobs } = require('orva')

module.exports.handler = async (event) => {
  // F2F call — B becomes a child span under A.
  const result = await invoke('send_email', { to: event.email })

  // Job enqueue — when this job runs (now or in 6 hours), the resulting
  // execution lands in the SAME trace as A.
  await jobs.enqueue('audit_log', { action: 'sent', to: event.email })

  return { statusCode: 200, body: 'ok' }
}`,Kt=`# Send the W3C traceparent header — Orva will adopt it as the trace root.
curl -H "traceparent: 00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01" \\
     https://orva.example.com/fn/myfn/

# Response always echoes:
# X-Trace-Id: tr_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`,Ft=`{
  "error": {
    "code": "VALIDATION",
    "message": "name must be lowercase and dash-separated",
    "request_id": "req_abc123"
  }
}`,zt="<YOUR_ORVA_TOKEN>",wo={__name:"Docs",setup(c){const O=On();ve.registerLanguage("python",Un),ve.registerLanguage("javascript",Ht),ve.registerLanguage("js",Ht),ve.registerLanguage("json",qn),ve.registerLanguage("bash",mt),ve.registerLanguage("shell",mt),ve.registerLanguage("sh",mt),ve.registerLanguage("http",Wn);const p=Y(()=>window.location.origin),E=[{id:"handler",num:"01",label:"Handler"},{id:"deploy",num:"02",label:"Deploy"},{id:"config",num:"03",label:"Config"},{id:"sdk",num:"04",label:"SDK"},{id:"schedules",num:"05",label:"Schedules"},{id:"webhooks",num:"06",label:"Webhooks"},{id:"mcp",num:"07",label:"MCP"},{id:"generate",num:"08",label:"AI prompt"},{id:"tracing",num:"09",label:"Tracing"},{id:"errors",num:"10",label:"Errors"}],W=Le("handler");let Z=null;Rn(()=>{if(typeof IntersectionObserver>"u")return;const h=new Set;Z=new IntersectionObserver(n=>{for(const C of n)C.isIntersecting?h.add(C.target.id):h.delete(C.target.id);for(const C of E)if(h.has(C.id)){W.value=C.id;break}},{rootMargin:"-20% 0px -70% 0px",threshold:0});for(const n of E){const C=document.getElementById(n.id);C&&Z.observe(C)}}),Nn(()=>{Z&&Z.disconnect()});const ee=Cn(),R=Le(!1);let K=null;const F=async()=>{await An()&&(R.value=!0,clearTimeout(K),K=setTimeout(()=>{R.value=!1},1500))},Q=Ve({setup(){return()=>k("svg",{viewBox:"0 0 256 255",width:"14",height:"14",xmlns:"http://www.w3.org/2000/svg"},[k("defs",null,[k("linearGradient",{id:"pyg1",x1:"0",y1:"0",x2:"1",y2:"1"},[k("stop",{offset:"0","stop-color":"#387EB8"}),k("stop",{offset:"1","stop-color":"#366994"})]),k("linearGradient",{id:"pyg2",x1:"0",y1:"0",x2:"1",y2:"1"},[k("stop",{offset:"0","stop-color":"#FFE052"}),k("stop",{offset:"1","stop-color":"#FFC331"})])]),k("path",{fill:"url(#pyg1)",d:"M126.9 12c-58.3 0-54.7 25.3-54.7 25.3l.1 26.2H128v8H50.5S12 67.2 12 126.1c0 58.9 33.6 56.8 33.6 56.8h19.4v-27.4s-1-33.6 33.1-33.6h55.9s32 .5 32-30.9V43.5S191.7 12 126.9 12zM95.7 29.9a10 10 0 0 1 0 20 10 10 0 0 1 0-20z"}),k("path",{fill:"url(#pyg2)",d:"M129.1 243c58.3 0 54.7-25.3 54.7-25.3l-.1-26.2H128v-8h77.5s38.5 4.4 38.5-54.5c0-58.9-33.6-56.8-33.6-56.8h-19.4v27.4s1 33.6-33.1 33.6H102s-32-.5-32 30.9v52S64.3 243 129.1 243zm30.4-17.9a10 10 0 0 1 0-20 10 10 0 0 1 0 20z"})])}}),z=Ve({setup(){return()=>k("svg",{viewBox:"0 0 256 280",width:"14",height:"14",xmlns:"http://www.w3.org/2000/svg"},[k("path",{fill:"#3F873F",d:"M128 0 12 67v146l116 67 116-67V67L128 0zm0 24.6 95 54.8v121.2l-95 54.8-95-54.8V79.4l95-54.8z"}),k("path",{fill:"#3F873F",d:"M128 64c-3 0-5.7.7-8 2.3L73 92c-5 2.7-8 8-8 13.6V169c0 5.6 3 10.7 8 13.5l13 7.4c6.3 3.1 8.5 3.1 11.4 3.1 9.4 0 14.8-5.7 14.8-15.6V117c0-1-.7-1.7-1.7-1.7H103c-1 0-1.7.7-1.7 1.7v60.2c0 4.4-4.5 8.7-11.8 5.1l-13.7-7.9a1.6 1.6 0 0 1-.8-1.4v-63.4c0-.6.3-1 .8-1.4l46.8-26.9c.4-.3 1-.3 1.4 0L171 110c.5.4.8.8.8 1.4V174a1.7 1.7 0 0 1-.8 1.4l-46.8 27c-.4.2-1 .2-1.4 0l-12-7.2c-.4-.2-.8-.2-1.2 0-3.4 1.9-4 2.2-7.2 3.3-.8.3-2 .7.4 2.1l15.7 9.3c2.5 1.4 5.3 2.2 8.2 2.2 2.9 0 5.7-.8 8.2-2.2L181 184c5-2.8 8-7.9 8-13.5V107c0-5.6-3-10.7-8-13.5l-46.7-26.7a17 17 0 0 0-6.3-2.8z"})])}}),I=Y(()=>[{label:"Python",lang:"python",code:`def handler(event):
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
};`}]),ne=Y(()=>[{label:"curl",lang:"bash",code:`curl -X POST ${p.value}/fn/<function_id> \\
  -H 'Content-Type: application/json' \\
  -d '{"name": "Orva"}'`},{label:"fetch",lang:"js",code:`const res = await fetch('${p.value}/fn/<function_id>', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ name: 'Orva' }),
});
console.log(await res.json());`},{label:"Python",lang:"python",code:`import httpx

r = httpx.post(
    "${p.value}/fn/<function_id>",
    json={"name": "Orva"},
)
print(r.json())`}]),X=[{id:"python314",name:"Python 3.14",entry:"handler.py",deps:"requirements.txt",icon:Q},{id:"python313",name:"Python 3.13",entry:"handler.py",deps:"requirements.txt",icon:Q},{id:"node24",name:"Node.js 24",entry:"handler.js",deps:"package.json",icon:z},{id:"node22",name:"Node.js 22",entry:"handler.js",deps:"package.json",icon:z}],L=[{field:"env_vars",purpose:"Plain config",body:"Plaintext config stored on the function record. Use for feature flags and non-secret settings.",icon:Dn,iconClass:"text-violet-300"},{field:"/secrets",purpose:"Encrypted",body:"AES-256-GCM at rest. Values decrypt only into the worker environment at spawn time.",icon:ot,iconClass:"text-emerald-300"},{field:"network_mode",purpose:"Egress control",body:"none = isolated loopback. egress = outbound HTTPS allowed; firewall blocklist applies.",icon:ft,iconClass:"text-sky-300"},{field:"auth_mode",purpose:"Invoke gate",body:"none = public. platform_key = require Orva API key. signed = require HMAC.",icon:Ln,iconClass:"text-violet-300"},{field:"rate_limit_per_min",purpose:"Per-IP throttle",body:"Optional cap for public or webhook-facing functions. Exceeding it returns 429.",icon:Bn,iconClass:"text-amber-300"}],ce=Y(()=>`curl -X POST ${p.value}/api/v1/functions \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"name":"hello","runtime":"python314","memory_mb":128,"cpus":0.5}'`),se=Y(()=>`tar czf code.tar.gz handler.py requirements.txt
curl -X POST ${p.value}/api/v1/functions/<function_id>/deploy \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -F code=@code.tar.gz`),le=Y(()=>`curl -X POST ${p.value}/api/v1/functions/<function_id>/secrets \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"key":"DATABASE_URL","value":"postgres://..."}'`),ye=Y(()=>`# generate signature
SECRET='your-shared-secret-stored-in-function-secrets'
TS=$(date +%s)
BODY='{"hello":"world"}'
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')

curl -X POST ${p.value}/fn/<function_id> \\
  -H "X-Orva-Timestamp: $TS" \\
  -H "X-Orva-Signature: sha256=$SIG" \\
  -H 'Content-Type: application/json' \\
  -d "$BODY"`),Ee=Y(()=>[{label:"curl",lang:"bash",note:"Create a daily-9am schedule for an existing function. payload is delivered as the invoke body.",code:`curl -X POST ${p.value}/api/v1/functions/<function_id>/cron \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{
    "cron_expr": "0 9 * * *",
    "enabled":   true,
    "payload":   {"task": "daily-summary"}
  }'`},{label:"Toggle / edit",lang:"bash",note:"PUT accepts any subset of {cron_expr, enabled, payload}; omitted fields keep their previous value. next_run_at is recomputed on expr changes.",code:`# pause
curl -X PUT ${p.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"enabled": false}'

# change schedule
curl -X PUT ${p.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"cron_expr": "*/15 * * * *"}'`},{label:"List & delete",lang:"bash",note:"GET /api/v1/cron lists every schedule across functions (with function_name JOIN); per-function uses the nested route.",code:`# all schedules
curl ${p.value}/api/v1/cron \\
  -H 'X-Orva-API-Key: <YOUR_KEY>'

# delete one
curl -X DELETE ${p.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>'`}]),oe=[{label:"Python",lang:"python",code:`from orva import kv

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
}`}],pe=[{label:"Python",lang:"python",code:`from orva import invoke, OrvaError

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
}`}],Re=[{label:"Python",lang:"python",code:`from orva import jobs

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
}`}],_e=[{name:"deployment.succeeded",when:"A function build finished and the new version is active."},{name:"deployment.failed",when:"A build failed or was rejected."},{name:"function.created",when:"A new function row was created via POST /api/v1/functions."},{name:"function.updated",when:"A function config was edited via PUT /api/v1/functions/{id} (status flips during a deploy do NOT fire this — see deployment.*)."},{name:"function.deleted",when:"A function was removed."},{name:"execution.error",when:"An invocation finished with status=error or 5xx."},{name:"cron.failed",when:"A scheduled run failed (bad expr, missing fn, dispatch error, or 5xx)."},{name:"job.succeeded",when:"A queued background job finished successfully."},{name:"job.failed",when:"A queued job exhausted its retries (terminal failure)."}],Ne=[{label:"Python",lang:"python",note:"Run on the receiver. Reject anything that fails verification — the signature ensures the request really came from this Orva instance.",code:`import hmac, hashlib, time

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
})`}],Ie=[{name:"http",desc:"Public HTTP request hit /fn/<id>/. Almost always a root span."},{name:"f2f",desc:"Another function called this one via orva.invoke(). Has a parent_span_id."},{name:"job",desc:"Background job runner picked up an enqueued job. Parent_span_id is whoever enqueued it."},{name:"cron",desc:"Scheduler fired a cron entry. Always a root span."},{name:"inbound",desc:"External webhook hit /webhook/{id}. Always a root span."},{name:"replay",desc:"Operator clicked Replay on a captured execution. Fresh trace, no link to original."},{name:"mcp",desc:"AI agent invoked the function via MCP invoke_function. Fresh trace."}],Me=[{code:"VALIDATION",when:"Bad request body or path parameter."},{code:"UNAUTHORIZED",when:"Missing or invalid API key / session cookie."},{code:"NOT_FOUND",when:"Function, deployment, or secret doesn't exist."},{code:"RATE_LIMITED",when:"Too many requests — check the Retry-After header."},{code:"VERSION_GCD",when:"Rollback target was garbage-collected."},{code:"INSUFFICIENT_DISK",when:"Host is below min_free_disk_mb."}],qe=Y(()=>{const h=(M,at)=>at.map(Pe=>`### ${M} — ${Pe.label}

${Pe.note?`> ${Pe.note}

`:""}\`\`\`${Pe.lang}
${Pe.code}
\`\`\``).join(`

`),n=`| Runtime | ID | Entrypoint | Dependencies |
|---|---|---|---|
`+X.map(M=>`| ${M.name} | \`${M.id}\` | \`${M.entry}\` | \`${M.deps}\` |`).join(`
`),C=`| Field | Purpose | Behaviour |
|---|---|---|
`+L.map(M=>`| \`${M.field}\` | ${M.purpose} | ${M.body} |`).join(`
`),v=`| Trigger | Meaning |
|---|---|
`+Ie.map(M=>`| \`${M.name}\` | ${M.desc} |`).join(`
`),$=`| Event | When it fires |
|---|---|
`+_e.map(M=>`| \`${M.name}\` | ${M.when} |`).join(`
`),ie=`| Code | When you see it |
|---|---|
`+Me.map(M=>`| \`${M.code}\` | ${M.when} |`).join(`
`);return`# Orva — Documentation

> Everything you need to write, deploy, and operate functions on Orva.
> Generated from the in-app Docs page (\`${p.value}/web/docs\`).

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

---

## Handler contract

One exported function receives the inbound HTTP event and returns an
HTTP-shaped response. The adapter handles serialization and headers.

${h("Handler",I.value)}

**Event shape:** \`method\`, \`path\`, \`headers\`, \`query\`, \`body\`.

**Response:** \`{ statusCode, headers, body }\`. Non-string bodies are
JSON-encoded by the adapter.

**Runtime env:** env vars and secrets land in \`process.env\` (Node) /
\`os.environ\` (Python).

${n}

---

## Deploy & invoke

The dashboard handles day-to-day work; these calls are for CI and
automation. Builds run async — poll \`/api/v1/deployments/<id>\` or
stream \`/api/v1/deployments/<id>/stream\` until \`phase: done\`.

### 1. Create the function row

\`\`\`bash
${ce.value}
\`\`\`

### 2. Upload code

\`\`\`bash
${se.value}
\`\`\`

### Invoke

${h("Invoke",ne.value)}

> **Custom routes:** attach a friendly path with \`POST /api/v1/routes\`.
> Reserved prefixes: \`/api/\` \`/fn/\` \`/mcp/\` \`/web/\` \`/_orva/\`.

---

## Configuration reference

Everything below lives on the function record. Secrets are stored
encrypted and only decrypt into the worker environment at spawn time.

${C}

### Set a secret

\`\`\`bash
${le.value}
\`\`\`

### Signed-invoke recipe (HMAC, opt-in)

\`\`\`bash
${ye.value}
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

${h("KV",oe)}

> Browse / inspect / edit / delete / set keys without leaving the
> dashboard at \`/web/functions/<name>/kv\`. REST mirror at
> \`GET/PUT/DELETE /api/v1/functions/<id>/kv[/<key>]\`. MCP tools:
> \`kv_list\` / \`kv_get\` / \`kv_put\` / \`kv_delete\`.

### Function-to-function — invoke()

${h("F2F",pe)}

### Background jobs — jobs.enqueue()

${h("Jobs",Re)}

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

${h("Cron",Ee.value)}

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

${$}

### Verify a delivery

${h("Verify",Ne)}

---

## MCP — Model Context Protocol

Same API surface the dashboard uses, exposed as 69 tools an agent can
call directly. API key permissions scope the available tool set.

- **Endpoint:** \`${p.value}/mcp\`
- **Auth header:** \`Authorization: Bearer <token>\`
  (fallback: \`X-Orva-API-Key: <token>\`)
- **Transport:** Streamable HTTP, MCP 2025-11-25.

> Generate a token from the Docs page in the dashboard, then drop it
> into your client config (Claude Code, Claude Desktop, Cursor, Cline,
> Codex, Windsurf, ChatGPT, etc.). Either header works against the
> same API key store with identical permission gating.

### Install snippets (primary clients)

${h("MCP",Se.value)}

### More clients (Cursor, VS Code, Codex CLI, OpenCode, Zed, Windsurf, ChatGPT)

${h("MCP (extra)",Ae.value)}

### Hand-edited config files

${h("MCP config",Ye.value)}

---

## System prompt for AI assistants

Paste the prompt below into ChatGPT, Claude, Gemini, Cursor, Copilot,
or any other AI tool to teach it Orva's full surface — handler
contract, runtimes, sandbox limits, the in-sandbox \`orva\` SDK
(kv / invoke / jobs), cron triggers, system-event webhooks, auth
modes, and production patterns. The model then turns "describe what I
want" into a pasteable handler on the first try.

\`\`\`text
${ee}
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
${jt}
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
${Ut}
\`\`\`

### Triggers

Each span carries a \`trigger\` label so the UI can show how the chain
started.

${v}

### External correlation (W3C traceparent)

Send a standard \`traceparent\` header on the inbound HTTP request and
Orva makes its trace a child of yours. The same trace_id is echoed
back as \`X-Trace-Id\` on every response, so external systems can
correlate without parsing bodies.

\`\`\`bash
${Kt}
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
${Ft}
\`\`\`

${ie}
`}),he=Le(!1);let ge=null;const We=async()=>{await Pt(qe.value)&&(he.value=!0,clearTimeout(ge),ge=setTimeout(()=>{he.value=!1},1500))},fe=Le(""),we=Le(!1),j=Y(()=>fe.value.slice(0,12)),N=Y(()=>fe.value||zt),ke=async()=>{if(!we.value){we.value=!0;try{const h=new Date().toISOString().slice(0,16).replace("T"," "),n=await Pn.post("/keys",{name:"MCP — "+h,permissions:["invoke","read","write","admin"]});fe.value=n.data.key}catch(h){console.error("mint mcp key failed",h),O.notify({title:"Could not mint key",message:h?.response?.data?.error?.message||h.message||"Unknown error",danger:!0})}finally{we.value=!1}}},Se=Y(()=>[{label:"Claude Code",lang:"bash",note:"Anthropic's `claude` CLI. Restart Claude Code afterwards; `/mcp` lists Orva's 57 tools.",code:`claude mcp add --transport http --scope user orva ${p.value}/mcp --header "Authorization: Bearer ${N.value}"`},{label:"curl",lang:"bash",note:"Talk to MCP directly. Step 1 returns a session id (Mcp-Session-Id) that Step 2 references.",code:`curl -sD - -X POST ${p.value}/mcp \\
  -H 'Authorization: Bearer ${N.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'

curl -sX POST ${p.value}/mcp \\
  -H 'Authorization: Bearer ${N.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H 'Mcp-Session-Id: <SID>' \\
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'`}]),Ae=Y(()=>[{label:"Claude Desktop",lang:"json",note:"Paste into ~/Library/Application Support/Claude/claude_desktop_config.json (macOS), %APPDATA%\\Claude\\claude_desktop_config.json (Windows), or ~/.config/Claude/claude_desktop_config.json (Linux). Restart Claude Desktop.",code:`{
  "mcpServers": {
    "orva": {
      "url": "${p.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${N.value}"
      }
    }
  }
}`},{label:"Cursor",lang:"bash",note:"Open the link in your browser. Cursor pops an approval dialog and writes ~/.cursor/mcp.json.",code:`cursor://anysphere.cursor-deeplink/mcp/install?name=orva&config=${$e.value}`},{label:"VS Code",lang:"bash",note:'User-scoped install via the Copilot-MCP `code --add-mcp` flag. Pick "Workspace" at the prompt to write .vscode/mcp.json instead.',code:`code --add-mcp '{"name":"orva","type":"http","url":"${p.value}/mcp","headers":{"Authorization":"Bearer ${N.value}"}}'`},{label:"Codex CLI",lang:"bash",note:"OpenAI's `codex` CLI. Writes to ~/.codex/config.toml.",code:`codex mcp add --transport streamable-http orva ${p.value}/mcp --header "Authorization: Bearer ${N.value}"`},{label:"OpenCode",lang:"bash",note:`Interactive add. Pick "Remote", paste ${p.value}/mcp, then add the header Authorization: Bearer ${N.value}.`,code:"opencode mcp add"},{label:"Zed",lang:"json",note:"Zed runs MCP as stdio subprocesses, so use the `mcp-remote` bridge. Paste under context_servers in ~/.config/zed/settings.json. Restart Zed.",code:`{
  "context_servers": {
    "orva": {
      "source": "custom",
      "command": "npx",
      "args": [
        "-y", "mcp-remote",
        "${p.value}/mcp",
        "--header", "Authorization:Bearer ${N.value}"
      ]
    }
  }
}`},{label:"Windsurf",lang:"json",note:"Paste into ~/.codeium/windsurf/mcp_config.json and reload Windsurf.",code:`{
  "mcpServers": {
    "orva": {
      "serverUrl": "${p.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${N.value}"
      }
    }
  }
}`},{label:"ChatGPT",lang:"text",note:"UI-only flow. Settings → Apps & Connectors → Developer mode → Add new connector. ChatGPT renders the tool catalog and confirms before destructive calls.",code:`URL:    ${p.value}/mcp
Auth:   API key (Bearer)
Token:  ${N.value}`}]),$e=Y(()=>{const h=JSON.stringify({url:p.value+"/mcp",headers:{Authorization:"Bearer "+N.value}});return typeof window.btoa=="function"?window.btoa(h):h}),Ye=Y(()=>[{label:"Cursor (global)",lang:"json",note:"Paste into ~/.cursor/mcp.json, or .cursor/mcp.json in your project root for a per-workspace install.",code:`{
  "mcpServers": {
    "orva": {
      "url": "${p.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${N.value}"
      }
    }
  }
}`},{label:"Cline",lang:"json",note:"In VS Code: open Cline → MCP icon → Configure MCP Servers. Cline writes cline_mcp_settings.json.",code:`{
  "mcpServers": {
    "orva": {
      "url": "${p.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${N.value}"
      },
      "disabled": false
    }
  }
}`}]),ae=Ve({name:"CodeBlock",props:{code:{type:String,required:!0},lang:{type:String,default:""}},setup(h){const n=Le(!1),C=async()=>{await Pt(h.code)&&(n.value=!0,setTimeout(()=>{n.value=!1},1200))},v=Y(()=>{const $=(h.lang||"").toLowerCase();if($&&ve.getLanguage($))try{return ve.highlight(h.code,{language:$,ignoreIllegals:!0}).value}catch{}return h.code.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;")});return()=>k("div",{class:"codeblock"},[k("div",{class:"codeblock-bar"},[k("span",{class:"codeblock-lang"},h.lang||""),k("button",{class:"codeblock-copy",onClick:C,title:"Copy code"},[n.value?k(ht,{class:"w-3 h-3"}):k(gt,{class:"w-3 h-3"}),n.value?"Copied":"Copy"])]),k("pre",{class:"codeblock-pre"},[k("code",{class:`hljs language-${(h.lang||"text").toLowerCase()}`,innerHTML:v.value})])])}}),re=Ve({name:"TabbedCode",props:{tabs:{type:Array,required:!0},storageKey:{type:String,default:""}},setup(h){const n=(()=>{try{if(h.storageKey){const $=localStorage.getItem(h.storageKey);if($&&h.tabs.some(ie=>ie.label===$))return $}}catch{}return h.tabs[0]?.label})(),C=Le(n),v=$=>{C.value=$;try{h.storageKey&&localStorage.setItem(h.storageKey,$)}catch{}};return()=>{const $=h.tabs.find(ie=>ie.label===C.value)||h.tabs[0];return k("div",{class:"tabbed"},[k("div",{class:"tabbed-tabs"},h.tabs.map(ie=>k("button",{key:ie.label,class:["tabbed-tab",{active:ie.label===C.value}],onClick:()=>v(ie.label)},ie.label))),$.note?k("div",{class:"tabbed-note"},$.note):null,k(ae,{code:$.code,lang:$.lang})])}}}),He=Ve({name:"Callout",props:{title:{type:String,default:""},icon:{type:[Object,Function],default:null}},setup(h,{slots:n}){return()=>k("div",{class:"callout"},[k("div",{class:"callout-head"},[h.icon?k(h.icon,{class:"callout-icon"}):null,h.title?k("span",null,h.title):null]),k("div",{class:"callout-body"},n.default?.())])}});return(h,n)=>{const C=Mn("router-link");return J(),xe("div",Xn,[t("header",Vn,[n[2]||(n[2]=t("div",{class:"docs-hero-bg","aria-hidden":"true"},null,-1)),t("div",Yn,[t("div",Zn,[n[0]||(n[0]=t("div",{class:"docs-hero-text"},[t("h1",{class:"docs-hero-title"}," Documentation "),t("p",{class:"docs-hero-sub"}," Everything you need to write, deploy, and operate functions on Orva. Handler contract, deploy + invoke, SDK, MCP, tracing, error taxonomy. ")],-1)),t("div",Jn,[t("button",{class:st(["docs-hero-copy",{copied:he.value}]),"aria-label":he.value?"Markdown copied to clipboard":"Copy entire docs page as Markdown",onClick:We},[he.value?(J(),Ke(_(ht),{key:0,class:"w-3.5 h-3.5"})):(J(),Ke(_(gt),{key:1,class:"w-3.5 h-3.5"})),m(" "+D(he.value?"Copied as Markdown":"Copy as Markdown"),1)],10,Qn)])]),t("nav",es,[n[1]||(n[1]=t("span",{class:"docs-hero-toc-label"},"Jump to",-1)),(J(),xe(Fe,null,ze(E,v=>t("a",{key:v.id,href:`#${v.id}`,class:st(["docs-hero-toc-link",{active:W.value===v.id}])},[t("span",ns,D(v.num),1),t("span",null,D(v.label),1)],10,ts)),64))])])]),t("section",ss,[n[4]||(n[4]=t("div",{class:"doc-section-head"},[t("span",{class:"doc-section-num"},"01"),t("div",null,[t("h2",{class:"doc-section-title"}," Handler contract "),t("p",{class:"doc-lede"}," One exported function receives the inbound HTTP event and returns an HTTP-shaped response. The adapter handles serialization and headers. ")])],-1)),T(_(re),{tabs:I.value,"storage-key":"docs.handler"},null,8,["tabs"]),n[5]||(n[5]=ue('<div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Event shape </div><div class="doc-card-body"><code class="doc-chip">method</code><code class="doc-chip">path</code><code class="doc-chip">headers</code><code class="doc-chip">query</code><code class="doc-chip">body</code></div></div><div class="doc-card"><div class="doc-microlabel"> Response </div><div class="doc-card-body"><code class="doc-chip">{ statusCode, headers, body }</code><p class="mt-1.5 text-foreground-muted"> Non-string bodies are JSON-encoded by the adapter. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Runtime env </div><div class="doc-card-body"> Env vars and secrets land in <code class="doc-chip">process.env</code> / <code class="doc-chip">os.environ</code>. </div></div></div>',1)),t("div",os,[t("table",as,[n[3]||(n[3]=t("thead",null,[t("tr",null,[t("th",null,"Runtime"),t("th",null,"ID"),t("th",{class:"hidden sm:table-cell"}," Entrypoint "),t("th",{class:"hidden md:table-cell"}," Dependencies ")])],-1)),t("tbody",null,[(J(),xe(Fe,null,ze(X,v=>t("tr",{key:v.id},[t("td",rs,[(J(),Ke(Bt(v.icon),{class:"shrink-0"})),m(" "+D(v.name),1)]),t("td",is,D(v.id),1),t("td",cs,D(v.entry),1),t("td",ls,D(v.deps),1)])),64))])])])]),t("section",ds,[n[10]||(n[10]=ue('<div class="doc-section-head"><span class="doc-section-num">02</span><div><h2 class="doc-section-title"> Deploy &amp; invoke </h2><p class="doc-lede"> The dashboard handles day-to-day work; these calls are for CI and automation. Builds run async — poll <code class="doc-chip">/api/v1/deployments/&lt;id&gt;</code> or stream <code class="doc-chip">/api/v1/deployments/&lt;id&gt;/stream</code> until <code class="doc-chip">phase: done</code>. </p></div></div>',1)),t("div",us,[t("div",ps,[n[6]||(n[6]=t("div",{class:"doc-step-label"},[t("span",{class:"doc-step-num"},"1"),m(" Create the function row ")],-1)),T(_(ae),{code:ce.value,lang:"bash"},null,8,["code"])]),t("div",hs,[n[7]||(n[7]=t("div",{class:"doc-step-label"},[t("span",{class:"doc-step-num"},"2"),m(" Upload code ")],-1)),T(_(ae),{code:se.value,lang:"bash"},null,8,["code"])])]),t("div",gs,[n[8]||(n[8]=t("div",{class:"doc-microlabel"}," Invoke ",-1)),T(_(re),{tabs:ne.value,"storage-key":"docs.invoke"},null,8,["tabs"])]),T(_(He),{icon:_(ft),title:"Custom routes"},{default:Ge(()=>[...n[9]||(n[9]=[m(" Attach a friendly path with ",-1),t("code",{class:"doc-chip"},"POST /api/v1/routes",-1),m(". Reserved prefixes: ",-1),t("code",{class:"doc-chip"},"/api/",-1),t("code",{class:"doc-chip"},"/fn/",-1),t("code",{class:"doc-chip"},"/mcp/",-1),t("code",{class:"doc-chip"},"/web/",-1),t("code",{class:"doc-chip"},"/_orva/",-1),m(". ",-1)])]),_:1},8,["icon"])]),t("section",fs,[n[14]||(n[14]=t("div",{class:"doc-section-head"},[t("span",{class:"doc-section-num"},"03"),t("div",null,[t("h2",{class:"doc-section-title"}," Configuration reference "),t("p",{class:"doc-lede"}," Everything below lives on the function record. Secrets are stored encrypted and only decrypt into the worker environment at spawn time. ")])],-1)),t("div",bs,[t("table",ms,[n[11]||(n[11]=t("thead",null,[t("tr",null,[t("th",null,"Field"),t("th",{class:"hidden sm:table-cell"}," Purpose "),t("th",null,"Behaviour")])],-1)),t("tbody",null,[(J(),xe(Fe,null,ze(L,v=>t("tr",{key:v.field,class:"align-top"},[t("td",vs,[(J(),Ke(Bt(v.icon),{class:st(["w-3.5 h-3.5 shrink-0",v.iconClass])},null,8,["class"])),t("code",null,D(v.field),1)]),t("td",ys,D(v.purpose),1),t("td",Es,D(v.body),1)])),64))])])]),t("div",_s,[n[12]||(n[12]=t("div",{class:"doc-microlabel"}," Set a secret ",-1)),T(_(ae),{code:le.value,lang:"bash"},null,8,["code"])]),t("details",ws,[t("summary",ks,[T(_(Dt),{class:"w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted"}),n[13]||(n[13]=m(" Signed-invoke recipe (HMAC, opt-in) ",-1))]),t("div",Ss,[T(_(ae),{code:ye.value,lang:"bash"},null,8,["code"])])])]),t("section",Ts,[n[20]||(n[20]=ue('<div class="doc-section-head"><span class="doc-section-num">04</span><div><h2 class="doc-section-title"> SDK from inside a function </h2><p class="doc-lede"> The bundled <code class="doc-chip">orva</code> module exposes three primitives every function can use without extra dependencies: a per-function key/value store, in-process calls to other Orva functions, and a fire-and-forget background job queue. Routes through the per-process internal token injected at worker spawn time. </p></div></div><div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.kv</code></div><div class="doc-card-body"><code class="doc-chip">put / get / delete / list</code><p class="mt-1.5 text-foreground-muted"> Per-function namespace on SQLite, optional TTL. </p></div></div><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.invoke</code></div><div class="doc-card-body"><code class="doc-chip">invoke(name, payload)</code><p class="mt-1.5 text-foreground-muted"> In-process call to another function. 8-deep call cap. </p></div></div><div class="doc-card"><div class="doc-microlabel"><code class="doc-chip">orva.jobs</code></div><div class="doc-card-body"><code class="doc-chip">jobs.enqueue(name, payload)</code><p class="mt-1.5 text-foreground-muted"> Fire-and-forget; persisted; retried with exp backoff. </p></div></div></div>',2)),t("div",xs,[n[15]||(n[15]=t("div",{class:"doc-microlabel"}," KV — get/put with TTL ",-1)),T(_(re),{tabs:oe,"storage-key":"docs.sdk.kv"}),n[16]||(n[16]=ue('<p class="text-xs text-foreground-muted"> Browse / inspect / edit / delete / set keys without leaving the dashboard at <code class="doc-chip">/web/functions/&lt;name&gt;/kv</code> (or click the <code class="doc-chip">KV</code> button in the editor&#39;s action bar). REST mirror at <code class="doc-chip">GET/PUT/DELETE /api/v1/functions/&lt;id&gt;/kv[/&lt;key&gt;]</code>; MCP tools <code class="doc-chip">kv_list</code> / <code class="doc-chip">kv_get</code> / <code class="doc-chip">kv_put</code> / <code class="doc-chip">kv_delete</code> for agents. </p>',1))]),t("div",As,[n[17]||(n[17]=t("div",{class:"doc-microlabel"}," Function-to-function — invoke() ",-1)),T(_(re),{tabs:pe,"storage-key":"docs.sdk.invoke"})]),t("div",Cs,[n[18]||(n[18]=t("div",{class:"doc-microlabel"}," Background jobs — jobs.enqueue() ",-1)),T(_(re),{tabs:Re,"storage-key":"docs.sdk.jobs"})]),T(_(He),{icon:_(ft),title:"Network mode"},{default:Ge(()=>[...n[19]||(n[19]=[m(" The SDK reaches orvad over loopback through the host gateway, so the function needs ",-1),t("code",{class:"doc-chip"},'network_mode: "egress"',-1),m(". On the default ",-1),t("code",{class:"doc-chip"},'"none"',-1),m(" the SDK throws ",-1),t("code",{class:"doc-chip"},"OrvaUnavailableError",-1),m(" with a clear hint. ",-1)])]),_:1},8,["icon"])]),t("section",Os,[t("div",Rs,[n[31]||(n[31]=t("span",{class:"doc-section-num"},"05",-1)),t("div",null,[n[30]||(n[30]=t("h2",{class:"doc-section-title"}," Schedules ",-1)),t("p",Ns,[n[22]||(n[22]=m(" Fire any function on a cron expression. The scheduler runs as part of the orvad process — no external service. Manage from the ",-1)),T(C,{to:"/cron",class:"text-foreground hover:text-white underline decoration-dotted underline-offset-4"},{default:Ge(()=>[...n[21]||(n[21]=[m("Schedules page",-1)])]),_:1}),n[23]||(n[23]=m(" or via the API. Standard 5-field cron with the usual shorthands (",-1)),n[24]||(n[24]=t("code",{class:"doc-chip"},"@daily",-1)),n[25]||(n[25]=m(", ",-1)),n[26]||(n[26]=t("code",{class:"doc-chip"},"@hourly",-1)),n[27]||(n[27]=m(", ",-1)),n[28]||(n[28]=t("code",{class:"doc-chip"},"*/5 * * * *",-1)),n[29]||(n[29]=m("). ",-1))])])]),T(_(re),{tabs:Ee.value,"storage-key":"docs.cron"},null,8,["tabs"]),T(_(He),{icon:_(In),title:"Cron-fired headers"},{default:Ge(()=>[...n[32]||(n[32]=[m(" Every cron-triggered invocation arrives at the function with ",-1),t("code",{class:"doc-chip"},"x-orva-trigger: cron",-1),m(" and ",-1),t("code",{class:"doc-chip"},"x-orva-cron-id: cron_…",-1),m(" on the event headers, so user code can branch on origin. ",-1)])]),_:1},8,["icon"])]),t("section",Is,[t("div",Ms,[n[37]||(n[37]=t("span",{class:"doc-section-num"},"06",-1)),t("div",null,[n[36]||(n[36]=t("h2",{class:"doc-section-title"}," Webhooks ",-1)),t("p",Ps,[n[34]||(n[34]=m(" Operator-managed subscriptions for system events. Configure URLs from the ",-1)),T(C,{to:"/webhooks",class:"text-foreground hover:text-white underline decoration-dotted underline-offset-4"},{default:Ge(()=>[...n[33]||(n[33]=[m("Webhooks page",-1)])]),_:1}),n[35]||(n[35]=m("; Orva delivers signed POSTs to them when matching events fire (deployments, function lifecycle, cron failures, job outcomes). Subscriptions are global, not per-function. ",-1))])])]),n[40]||(n[40]=ue('<div class="grid grid-cols-1 md:grid-cols-3 gap-3"><div class="doc-card"><div class="doc-microlabel"> Headers </div><div class="doc-card-body"><code class="doc-chip">X-Orva-Event</code><code class="doc-chip">X-Orva-Delivery-Id</code><code class="doc-chip">X-Orva-Timestamp</code><code class="doc-chip">X-Orva-Signature</code></div></div><div class="doc-card"><div class="doc-microlabel"> Signature </div><div class="doc-card-body"><code class="doc-chip">sha256=hex(hmac(secret, ts.body))</code><p class="mt-1.5 text-foreground-muted"> Same shape as Stripe / signed-invoke. Receivers verify with the secret returned at create time. </p></div></div><div class="doc-card"><div class="doc-microlabel"> Retries </div><div class="doc-card-body"><code class="doc-chip">5 attempts</code><code class="doc-chip">exp backoff (≤ 1h)</code><p class="mt-1.5 text-foreground-muted"> Receiver must 2xx within 15s. </p></div></div></div>',1)),t("div",Bs,[t("table",Ds,[n[38]||(n[38]=t("thead",null,[t("tr",null,[t("th",null,"Event"),t("th",null,"When it fires")])],-1)),t("tbody",null,[(J(),xe(Fe,null,ze(_e,v=>t("tr",{key:v.name},[t("td",Ls,[t("code",null,D(v.name),1)]),t("td",$s,D(v.when),1)])),64))])])]),t("div",Hs,[n[39]||(n[39]=t("div",{class:"doc-microlabel"}," Verify a delivery ",-1)),T(_(re),{tabs:Ne,"storage-key":"docs.webhooks.verify"})])]),t("section",js,[n[50]||(n[50]=t("div",{class:"doc-section-head"},[t("span",{class:"doc-section-num"},"07"),t("div",null,[t("h2",{class:"doc-section-title"}," MCP — Model Context Protocol "),t("p",{class:"doc-lede"}," Same API surface the dashboard uses, exposed as 57 tools an agent can call directly. API key permissions scope the available tool set. ")])],-1)),t("div",Us,[t("div",Ks,[n[41]||(n[41]=t("div",{class:"doc-microlabel"}," Endpoint ",-1)),t("div",Fs,[t("code",zs,D(p.value)+"/mcp",1)])]),n[42]||(n[42]=ue('<div class="doc-card"><div class="doc-microlabel"> Auth header </div><div class="doc-card-body"><code class="doc-chip break-all">Authorization: Bearer &lt;token&gt;</code><p class="mt-1.5 text-foreground-muted"> Or as a fallback: <code class="doc-chip">X-Orva-API-Key: &lt;token&gt;</code></p></div></div><div class="doc-card"><div class="doc-microlabel"> Transport </div><div class="doc-card-body"><code class="doc-chip">Streamable HTTP</code><code class="doc-chip">MCP 2025-11-25</code></div></div>',2))]),T(_(He),{icon:_(ot),title:"Two header formats; same auth"},{default:Ge(()=>[...n[43]||(n[43]=[m(" Either header works against the same API key store with identical permission gating. ",-1),t("code",{class:"doc-chip"},"Authorization: Bearer",-1),m(" is the MCP / OAuth 2 spec form — every MCP SDK (Claude Code, Claude Desktop, Cursor, mcp-remote, Python ",-1),t("code",{class:"doc-chip"},"mcp",-1),m(") configures it natively, so prefer it for new setups. ",-1),t("code",{class:"doc-chip"},"X-Orva-API-Key",-1),m(" is the same header the REST API accepts — useful when a tool reuses an existing Orva REST integration. Internally both paths SHA-256-hash the token and look it up against the same ",-1),t("code",{class:"doc-chip"},"api_keys",-1),m(" table. ",-1)])]),_:1},8,["icon"]),t("div",Gs,[t("div",qs,[T(_(ot),{class:"w-4 h-4 shrink-0 text-foreground-muted"}),fe.value?(J(),xe("span",Xs,[n[46]||(n[46]=m(" Token minted: ",-1)),t("code",Vs,D(j.value)+"…",1),n[47]||(n[47]=m(" — shown once, copy now. ",-1))])):(J(),xe("span",Ws,[n[44]||(n[44]=m(" Snippets show ",-1)),t("code",{class:"doc-chip"},D(zt)),n[45]||(n[45]=m(". Mint a token to substitute it everywhere. ",-1))]))]),t("button",{class:"doc-token-btn",disabled:we.value,onClick:ke},[T(_(ot),{class:"w-3.5 h-3.5"}),m(" "+D(fe.value?"Mint another":we.value?"Minting…":"Generate token"),1)],8,Ys)]),T(_(re),{tabs:Se.value,"storage-key":"docs.mcp.install"},null,8,["tabs"]),t("details",Zs,[t("summary",Js,[T(_(Dt),{class:"w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted"}),n[48]||(n[48]=m(" More clients (Cursor, VS Code, Codex CLI, OpenCode, Zed, Windsurf, ChatGPT, manual config) ",-1))]),t("div",Qs,[T(_(re),{tabs:Ae.value,"storage-key":"docs.mcp.install.more"},null,8,["tabs"]),n[49]||(n[49]=t("div",{class:"doc-microlabel pt-1"}," Hand-edited config files ",-1)),T(_(re),{tabs:Ye.value,"storage-key":"docs.mcp.manual"},null,8,["tabs"])])])]),t("section",eo,[n[51]||(n[51]=ue('<div class="doc-section-head"><span class="doc-section-num">08</span><div><h2 class="doc-section-title"> System prompt for AI assistants </h2><p class="doc-lede"> Paste the prompt below into ChatGPT, Claude, Gemini, Cursor, Copilot, or any other AI tool to teach it Orva&#39;s full surface — handler contract, runtimes, sandbox limits, the in-sandbox <code class="doc-chip">orva</code> SDK (kv / invoke / jobs), cron triggers, system-event webhooks, auth modes, and production patterns. The model then turns &quot;describe what I want&quot; into a pasteable handler on the first try. </p></div></div>',1)),t("div",to,[t("button",{class:st(["ai-copy-btn",{copied:R.value}]),onClick:F},[R.value?(J(),Ke(_(ht),{key:0,class:"w-3.5 h-3.5"})):(J(),Ke(_(gt),{key:1,class:"w-3.5 h-3.5"})),m(" "+D(R.value?"Copied":"Copy system prompt"),1)],2)]),T(_(ae),{code:_(ee),lang:"text"},null,8,["code"])]),t("section",no,[n[53]||(n[53]=ue('<div class="doc-section-head"><span class="doc-section-num">09</span><div><h2 class="doc-section-title"> Tracing </h2><p class="doc-lede"> Every invocation chain is recorded as a causal trace — automatically, with <strong>zero changes to your function code</strong>. HTTP requests, F2F invokes, jobs, cron, inbound webhooks, and replays all stitch into the same tree. The dashboard renders it as a waterfall at <code class="doc-chip">/traces</code>. </p></div></div><p class="doc-prose"> Each execution row IS a span. Spans share a <code class="doc-chip">trace_id</code>; child spans point at their parent via <code class="doc-chip">parent_span_id</code>. You don&#39;t instantiate spans, you don&#39;t import a tracer — you just write your handler and the platform plumbs IDs through every internal hop. </p><h3 class="doc-h3">What user code sees</h3><p class="doc-prose"> Two env vars are stamped per invocation. Read them only if you want to log the trace_id alongside your own messages — they&#39;re optional. </p>',4)),T(_(ae),{code:jt,lang:"text"}),n[54]||(n[54]=t("h3",{class:"doc-h3"},"Automatic propagation",-1)),n[55]||(n[55]=t("p",{class:"doc-prose"},[m(" When a function calls another via the SDK, the trace context flows through automatically. The called function becomes a child span of the caller; both share the same "),t("code",{class:"doc-chip"},"trace_id"),m(". ")],-1)),T(_(ae),{code:Ut,lang:"js"}),n[56]||(n[56]=ue('<p class="doc-prose"> Job enqueues work the same way: <code class="doc-chip">orva.jobs.enqueue()</code> records the trace context on the job row. When the scheduler picks the job up later, the resulting execution lands in the same trace as the function that enqueued it — even if the gap is hours or days. </p><h3 class="doc-h3">Triggers</h3><p class="doc-prose"> Each span carries a <code class="doc-chip">trigger</code> label so the UI can show how the chain started. </p>',3)),t("div",so,[t("table",oo,[n[52]||(n[52]=t("thead",null,[t("tr",null,[t("th",null,"Trigger"),t("th",null,"Meaning")])],-1)),t("tbody",null,[(J(),xe(Fe,null,ze(Ie,v=>t("tr",{key:v.name},[t("td",ao,[t("code",null,D(v.name),1)]),t("td",ro,D(v.desc),1)])),64))])])]),n[57]||(n[57]=t("h3",{class:"doc-h3"},"External correlation (W3C traceparent)",-1)),n[58]||(n[58]=t("p",{class:"doc-prose"},[m(" Send a standard "),t("code",{class:"doc-chip"},"traceparent"),m(" header on the inbound HTTP request and Orva makes its trace a child of yours. The same trace_id is echoed back as "),t("code",{class:"doc-chip"},"X-Trace-Id"),m(" on every response, so external systems can correlate without parsing bodies. ")],-1)),T(_(ae),{code:Kt,lang:"bash"}),n[59]||(n[59]=ue('<h3 class="doc-h3">Outlier detection</h3><p class="doc-prose"> Each function maintains an in-memory rolling P95 baseline over its last 100 successful warm executions. An invocation is flagged as an outlier when it has at least 20 baseline samples AND its duration exceeds <strong>P95 × 2</strong>. Cold starts and errors are excluded from the baseline so a flapping function can&#39;t drag it down. The flag and baseline P95 are stored on the execution row and rendered as an amber flag icon next to the span. </p><h3 class="doc-h3">Where to look</h3><ul class="doc-list"><li><code class="doc-chip">/traces</code> — list of recent traces, filterable by function / status / outlier-only.</li><li><code class="doc-chip">/traces/:id</code> — waterfall + per-span detail. Click a span to jump to its execution in the Invocations log.</li><li><code class="doc-chip">GET /api/v1/traces/{id}</code> — full span tree as JSON. Pair with <code class="doc-chip">list_traces</code> / <code class="doc-chip">get_trace</code> MCP tools for AI agents.</li><li><code class="doc-chip">GET /api/v1/functions/{id}/baseline</code> — current P95/P99/mean for a function.</li></ul>',4))]),t("section",io,[n[61]||(n[61]=ue('<div class="doc-section-head"><span class="doc-section-num">10</span><div><h2 class="doc-section-title"> Errors &amp; recovery </h2><p class="doc-lede"> Every error response uses the same envelope so log scrapers and retries can match on <code class="doc-chip">code</code>. Deploys are content-addressed; rollback retargets the active version pointer and refreshes warm workers. </p></div></div>',1)),T(_(ae),{code:Ft,lang:"json"}),t("div",co,[t("table",lo,[n[60]||(n[60]=t("thead",null,[t("tr",null,[t("th",null,"Code"),t("th",null,"When you see it")])],-1)),t("tbody",null,[(J(),xe(Fe,null,ze(Me,v=>t("tr",{key:v.code},[t("td",uo,[t("code",null,D(v.code),1)]),t("td",po,D(v.when),1)])),64))])])])])])}}};export{wo as default};
