// Does every route actually render, without throwing?
//
// The API suites prove the server answers. They never open the dashboard, so a
// view that throws on mount, references an undefined store field, or renders a
// component the runtime-only Vue build cannot compile is invisible to them:
// the HTTP request that fed it returned 200 either way.
//
// That last case is not hypothetical. A component defined with a `template:`
// string renders nothing at all under the runtime-only build, silently, and it
// shipped that way in the Invocations drawer.

export const meta = {
  id: 'smoke',
  title: 'Every route renders without runtime errors',
}

export function onPage({ page, route, viewport, errors, report }) {
  const where = `${viewport.name} ${route.name}`

  report.record('smoke', where, 'no console or page errors',
    errors.map((e) => e.slice(0, 180)))

  return page.evaluate(() => {
    const main = document.querySelector('main') || document.body
    const text = (main.innerText || '').trim()
    return {
      // A view that mounted but rendered nothing looks identical to a working
      // one in a screenshot diff and in any HTTP check.
      empty: text.length < 2,
      // Vue leaves unresolved interpolation in the DOM when a render fails
      // partway; catching it here is cheaper than reading every template.
      rawMustache: /\{\{[^}]{1,60}\}\}/.test(main.innerHTML),
      title: document.title,
    }
  }).then((r) => {
    report.record('smoke', where, 'renders visible content',
      r.empty ? ['the main region rendered no text at all'] : [])
    report.record('smoke', where, 'no unrendered template syntax',
      r.rawMustache ? ['found literal {{ }} in the DOM, so a render failed partway'] : [])
    report.record('smoke', where, 'document has a title',
      r.title && r.title.trim() ? [] : ['<title> is empty'])
  })
}
