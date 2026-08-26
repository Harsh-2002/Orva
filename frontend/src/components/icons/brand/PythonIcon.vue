<!--
  Python brand mark, two-tone, in the official palette: #3776AB on the
  upper-left snake, #FFD43B on the lower-right. Geometry is Simple Icons
  (https://simpleicons.org), CC0-licensed, used nominatively to identify a
  function's runtime.

  It used to be a single currentColor path tinted with --color-runtime-python,
  and at 14 px that reads as an unidentifiable blob: the whole shape of this
  mark is two interlocking snakes, and without the colour split there is
  nothing to tell them apart. The two-tone version is recognisable at the size
  it is actually drawn, which is the entire job of putting a mark there instead
  of the word.

  fill-rule="evenodd" is load-bearing: each snake carries its own eye as a
  second subpath, and evenodd is what punches it through instead of filling it.

  The two paths were derived from the single-path original by splitting on
  subpath boundaries and converting each relative 'm' to an absolute 'M'. A
  relative move after 'z' is relative to that subpath's start, not to the
  origin, so they cannot simply be cut apart.
-->
<template>
  <svg
    xmlns="http://www.w3.org/2000/svg"
    viewBox="0 0 24 24"
    fill-rule="evenodd"
    aria-hidden="true"
  >
    <path
      d="M14.25 0.18l.9.2.73.26.59.3.45.32.34.34.25.34.16.33.1.3.04.26.02.2-.01.13V8.5l-.05.63-.13.55-.21.46-.26.38-.3.31-.33.25-.35.19-.35.14-.33.1-.3.07-.26.04-.21.02H8.77l-.69.05-.59.14-.5.22-.41.27-.33.32-.27.35-.2.36-.15.37-.1.35-.07.32-.04.27-.02.21v3.06H3.17l-.21-.03-.28-.07-.32-.12-.35-.18-.36-.26-.36-.36-.35-.46-.32-.59-.28-.73-.21-.88-.14-1.05-.05-1.23.06-1.22.16-1.04.24-.87.32-.71.36-.57.4-.44.42-.33.42-.24.4-.16.36-.1.32-.05.24-.01h.16l.06.01h8.16v-.83H6.18l-.01-2.75-.02-.37.05-.34.11-.31.17-.28.25-.26.31-.23.38-.2.44-.18.51-.15.58-.12.64-.1.71-.06.77-.04.84-.02 1.27.05zM7.95 2.16l-.23.33-.08.41.08.41.23.34.33.22.41.09.41-.09.33-.22.23-.34.08-.41-.08-.41-.23-.33-.33-.22-.41-.09-.41.09z"
      fill="#3776AB"
    />
    <path
      d="M21.04 6.11l.28.06.32.12.35.18.36.27.36.35.35.47.32.59.28.73.21.88.14 1.04.05 1.23-.06 1.23-.16 1.04-.24.86-.32.71-.36.57-.4.45-.42.33-.42.24-.4.16-.36.09-.32.05-.24.02-.16-.01h-8.22v.82h5.84l.01 2.76.02.36-.05.34-.11.31-.17.29-.25.25-.31.24-.38.2-.44.17-.51.15-.58.13-.64.09-.71.07-.77.04-.84.01-1.27-.04-1.07-.14-.9-.2-.73-.25-.59-.3-.45-.33-.34-.34-.25-.34-.16-.33-.1-.3-.04-.25-.02-.2.01-.13v-5.34l.05-.64.13-.54.21-.46.26-.38.3-.32.33-.24.35-.2.35-.14.33-.1.3-.06.26-.04.21-.02.13-.01h5.84l.69-.05.59-.14.5-.21.41-.28.33-.32.27-.35.2-.36.15-.36.1-.35.07-.32.04-.28.02-.21V6.07h2.09l.14.01zM14.57 20.36l-.23.33-.08.41.08.41.23.33.33.23.41.08.41-.08.33-.23.23-.33.08-.41-.08-.41-.23-.33-.33-.23-.41-.08-.41.08z"
      fill="#FFD43B"
    />
  </svg>
</template>
