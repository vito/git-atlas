// Drives index.html in a headless browser; see run.sh. Writes the outcome
// into #smoke-result ("PASS …" or "FAIL …") and details into #smoke-log.
// PRs are picked from the data, so the test survives PRs merging.
(function () {
  'use strict';
  var G = window.GitEvo, log = [], fails = 0;
  function ok(cond, what) { log.push((cond ? 'ok   ' : 'FAIL ') + what); if (!cond) fails++; }
  function $(s) { return document.querySelector(s); }
  function $$(s) { return document.querySelectorAll(s); }
  function hash() { return decodeURIComponent(location.hash); }
  function finish() {
    var r = document.createElement('div');
    r.id = 'smoke-result';
    r.textContent = (fails ? 'FAIL ' + fails + ' of ' : 'PASS ') + log.length + ' assertions';
    var l = document.createElement('div');
    l.id = 'smoke-log';
    l.textContent = log.join('|').replace(/[<>]/g, '');
    document.body.appendChild(r);
    document.body.appendChild(l);
  }
  var steps = [], i = 0;
  function step(f) { steps.push(f); }
  function next() {
    if (i >= steps.length) return finish();
    try { steps[i++](); } catch (e) { ok(false, 'step ' + i + ' threw: ' + e); }
    setTimeout(next, 60);
  }
  function go(h) { location.hash = h; }

  var D = G && G.data, api, tabPR, unselected, cellPR, findingPR;
  step(function () {
    ok(!!G, 'renderer loaded (window.GitEvo)');
    var errs = 0, tabs = 0;
    // Every field, every tab and every page renders.
    D.apis.forEach(function (a) {
      G.tabsOf(a.impl).forEach(function (t) {
        tabs++;
        try { G.render('#' + a.id + '/' + t.pr); } catch (e) { errs++; log.push('render ' + a.id + '/' + t.pr + ': ' + e); }
      });
    });
    ['#timeline', '#matrix', '#findings', '#about', '#glossary', '#compare=' + D.apis[0].id + ',' + D.apis[1].id].forEach(function (h) {
      try { G.render(h); } catch (e) { errs++; log.push('render ' + h + ': ' + e); }
    });
    ok(errs === 0 && tabs >= D.apis.length, 'rendered ' + tabs + ' tabs and every page without errors');
    var open = D.prs.filter(function (p) { return p.state === 'open'; }).map(function (p) { return p.id; });
    ok(G.selection().join() === open.join(), 'default selection is the open PRs (' + (open.join(',') || 'none') + ')');
    var aliases = Object.keys(D.aliases);
    ok(!aliases.length || G.render('#' + aliases[0]).html.indexOf(D.aliases[aliases[0]]) >= 0, 'old deep links resolve through aliases');
    ok((G.render('#glossary').html.match(/<dt>/g) || []).length === D.glossary.length, 'glossary lists every term');
    var m = G.render('#matrix').html, cells = 0;
    D.matrix.forEach(function (r) { cells += Object.keys(r.cells).filter(function (k) { return r.cells[k].s !== 'na'; }).length; });
    ok((m.match(/class="cell (yes|partial|gap|pend)/g) || []).length >= cells, 'matrix shows every non-na cell (' + cells + ')');
    // Fixtures from the data.
    api = D.apis.filter(function (a) { return G.tabsOf(a.impl).length >= 3; })[0];
    var ts = G.tabsOf(api.impl);
    tabPR = ts[ts.length - 2].pr;
    unselected = D.prs.filter(function (p) { return open.indexOf(p.id) < 0 && p.state === 'merged'; })[0].id;
    D.matrix.some(function (r) { return Object.keys(r.cells).some(function (k) { return (cellPR = (r.cells[k].prs || [])[0]); }); });
    findingPR = D.findings[0].discovered_in;
    go('#' + api.id + '?hl=' + tabPR);
  });
  step(function () {
    ok($$('.tab.hl').length === 1, 'one tab highlighted on ' + api.id + ' (#' + tabPR + ')');
    ok($('#prsws .sw') !== null, 'the button shows the selection swatch');
    ok($('#prmenu').hidden, 'PR menu starts closed');
    $('#prbtn').click();
  });
  step(function () {
    ok(!$('#prmenu').hidden, 'PR menu opens');
    var rows = $$('#prlist input[type=checkbox]');
    ok(rows.length === D.prs.length, 'menu lists every PR (' + rows.length + ')');
    var cb = $('#prlist input[data-pr="' + unselected + '"]');
    ok(!!cb && !cb.checked, '#' + unselected + ' starts unselected');
    cb.checked = true;
    cb.dispatchEvent(new Event('change', { bubbles: true }));
  });
  step(function () {
    ok(G.selection().indexOf(unselected) >= 0 && G.selection().indexOf(tabPR) >= 0, 'checking #' + unselected + ' adds it to the selection');
    ok(/[?&]hl=/.test(hash()) && hash().indexOf(unselected) >= 0, 'selection is in the URL hash: ' + hash());
    var link = $('#list a.item');
    ok(link && link.getAttribute('href').indexOf('hl=') >= 0, 'sidebar links carry the selection');
    ok($('.topbar a.page[data-page="matrix"]').getAttribute('href').indexOf('hl=') >= 0, 'top bar links carry the selection');
    go('#timeline?hl=' + unselected + ',' + tabPR);
  });
  step(function () {
    ok(G.selection().length === 2, 'hash selection is applied on load');
    ok($$('#view tr.hl').length === 2, 'timeline highlights two rows');
    ok($$('#view a.dot.hl').length >= 2, 'timeline squares use PR colors');
    go('#matrix?hl=' + cellPR);
  });
  step(function () {
    ok($$('#view .cell.hl').length >= 1, 'matrix rings cells changed by #' + cellPR + ' (' + $$('#view .cell.hl').length + ')');
    $('#view .cell.hl').click();
  });
  step(function () {
    ok(/judged at/.test($('#note').textContent), 'matrix note shows the commit it was judged at');
    go('#findings?hl=' + findingPR);
  });
  step(function () {
    ok($$('#view .finding.hl').length >= 1, 'findings found in #' + findingPR + ' are highlighted');
    go('#' + api.id + '?hl=');
  });
  step(function () {
    ok(G.selection().length === 0 && $$('.tab.hl').length === 0, 'empty hl= clears every highlight');
    $('.prtools a[data-hl="all"]').click();
  });
  step(function () {
    ok(G.selection().length === D.prs.length, '"all" selects every PR');
    $('.prtools a[data-hl="open"]').click();
  });
  step(function () {
    ok(G.selection().join() === G.defaultSelection.join(), '"open" restores the default selection');
    ok(hash().indexOf('hl=') < 0, 'the default selection keeps the hash clean');
    ok($$('.where a[href^="https://github.com/"]').length > 0, 'code pointers link to GitHub');
    document.body.click();
  });
  step(function () {
    ok($('#prmenu').hidden, 'clicking elsewhere closes the menu');
  });
  setTimeout(next, 50);
})();
