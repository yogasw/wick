// access.js — the reach badge, its user modal, the tag-usage modal, and the
// per-page search box. Vanilla and dependency-free, like the rest of
// /modules/admin/js (the admin panel has no htmx and no framework).
(function () {
  "use strict";

  // ── Modal ────────────────────────────────────────────────────────────────

  function modal() {
    return document.getElementById("access-modal");
  }

  function openModal(title, subtitle, bodyHTML) {
    var m = modal();
    if (!m) return;
    document.getElementById("access-modal-title").textContent = title;
    document.getElementById("access-modal-subtitle").textContent = subtitle || "";
    document.getElementById("access-modal-body").innerHTML = bodyHTML;
    m.classList.remove("hidden");
    m.classList.add("flex");
  }

  function closeModal() {
    var m = modal();
    if (!m) return;
    m.classList.add("hidden");
    m.classList.remove("flex");
  }

  function loading(title, subtitle) {
    openModal(title, subtitle, '<p class="text-black-700 dark:text-black-600">Loading…</p>');
  }

  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  function chip(text, tone) {
    var cls =
      tone === "warn"
        ? "border-amber-400 dark:border-amber-700 text-amber-700 dark:text-amber-400"
        : "border-white-400 dark:border-navy-600 text-black-800 dark:text-black-600";
    return (
      '<span class="inline-flex items-center rounded-full border px-2 py-0.5 text-[10px] ' +
      cls +
      '">' +
      esc(text) +
      "</span>"
    );
  }

  function userRows(users) {
    if (!users || !users.length) {
      return '<p class="text-black-700 dark:text-black-600">Nobody.</p>';
    }
    var out = '<ul class="divide-y divide-white-300 dark:divide-navy-600">';
    users.forEach(function (u) {
      var via = (u.via_tags || [])
        .map(function (t) {
          return chip(t);
        })
        .join(" ");
      var hay = [u.name, u.email, u.id, u.role]
        .concat(u.via_tags || [])
        .join(" ")
        .toLowerCase();
      out +=
        '<li data-modal-row="' + esc(hay) + '" class="flex flex-wrap items-center gap-2 py-2">' +
        '<span class="font-medium text-black-900 dark:text-white-100">' +
        esc(u.name || u.email) +
        "</span>" +
        '<span class="text-xs text-black-700 dark:text-black-600">' +
        esc(u.email) +
        "</span>" +
        (u.role === "admin"
          ? '<span class="rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-[9px] uppercase tracking-wide text-black-700 dark:text-black-600" title="account role">admin</span>'
          : "") +
        // "via" spelled out: the role badge above sits right next to the name
        // and was being read as the REASON this person has access, which it is
        // not — the reason is always what follows this label.
        '<span class="ml-auto flex flex-wrap items-center gap-1">' +
        (via ? '<span class="text-[10px] text-black-700 dark:text-black-600">via</span>' : "") +
        via +
        "</span>" +
        "</li>";
    });
    return out + "</ul>";
  }


  // modalSearch is the filter inside a modal. The lists behind a reach badge
  // are as long as the install is big — 30 users, 40 granted items — so the
  // question "is Hana in here" needs an answer that is not scrolling.
  // Rendered only when the list is long enough to be worth filtering.
  function modalSearch(count, what) {
    if (count < 8) return "";
    return (
      '<input type="search" class="access-modal-search mb-3 w-full rounded-md border ' +
      'border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm ' +
      'text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500" ' +
      'placeholder="Filter ' + esc(what) + '…" autocomplete="off"/>' +
      '<p class="access-modal-count mb-2 text-[11px] text-black-700 dark:text-black-600"></p>'
    );
  }

  // filterModal hides the rows of every list in the modal that do not match.
  // Section headings stay put: a heading with nothing under it still tells you
  // that surface has no match, which is information.
  function filterModal(input) {
    var body = document.getElementById("access-modal-body");
    if (!body) return;
    var q = input.value.trim().toLowerCase();
    var rows = body.querySelectorAll("[data-modal-row]");
    var shown = 0;
    rows.forEach(function (row) {
      var match = !q || (row.getAttribute("data-modal-row") || "").indexOf(q) !== -1;
      row.classList.toggle("hidden", !match);
      if (match) shown++;
    });
    var out = body.querySelector(".access-modal-count");
    if (out) out.textContent = q ? shown + " of " + rows.length + " shown" : "";
  }

  // ── Access badge → who can see this row ──────────────────────────────────

  function showAccess(path) {
    loading("Who can access this", path);
    fetch("/admin/access/users?path=" + encodeURIComponent(path), {
      headers: { Accept: "application/json" },
    })
      .then(function (r) {
        return r.json();
      })
      .then(function (d) {
        if (d.error) {
          openModal("Who can access this", path, '<p class="text-neg-400">' + esc(d.error) + "</p>");
          return;
        }
        var head = "";
        if (d.tags_inert) {
          // The tag picker on this page writes tags nothing reads. Saying it
          // here is the only place an admin finds out their share did nothing.
          head +=
            '<p class="mb-3 rounded-md border border-amber-400 dark:border-amber-700 bg-amber-50 dark:bg-navy-800 px-3 py-2 text-amber-700 dark:text-amber-400">' +
            "This surface never reads its access tags — " +
            (d.tags || [])
              .map(function (t) {
                return chip(t, "warn");
              })
              .join(" ") +
            " grant nobody access. Only the owner (and admins) reach it.</p>";
        }
        if (d.owner_scoped) {
          head +=
            '<p class="mb-3 text-black-800 dark:text-black-600">Untagged means <strong>owner-only</strong> on this ' +
            esc(d.kind_one || "item") +
            " — not public. " +
            (d.users || []).length +
            " user(s) reach it.</p>";
        } else if (d.public) {
          head =
            '<p class="mb-3 text-black-800 dark:text-black-600">No access tag on this ' +
            esc(d.kind_one || "item") +
            " — <strong>every approved user</strong> can see it (" +
            (d.users || []).length +
            ").</p>";
        } else {
          head =
            '<p class="mb-3 text-black-800 dark:text-black-600">Restricted to ' +
            (d.tags || [])
              .map(function (t) {
                return chip(t);
              })
              .join(" ") +
            " — " +
            (d.users || []).length +
            " user(s).</p>";
          if (!(d.users || []).length) {
            head +=
              '<p class="mb-3 rounded-md border border-amber-400 dark:border-amber-700 bg-amber-50 dark:bg-navy-800 px-3 py-2 text-amber-700 dark:text-amber-400">' +
              "Nobody carries these tags, so no non-admin can reach it.</p>";
          }
        }
        openModal(
          "Who can access this",
          d.path || path,
          head + modalSearch((d.users || []).length, "users") + userRows(d.users)
        );
      })
      .catch(function (e) {
        openModal("Who can access this", path, '<p class="text-neg-400">' + esc(e.message) + "</p>");
      });
  }

  // ── Tag info → who carries it, what it opens ─────────────────────────────

  function showTagUsage(tagID) {
    loading("Tag detail", "");
    fetch("/admin/tags/" + encodeURIComponent(tagID) + "/usage", {
      headers: { Accept: "application/json" },
    })
      .then(function (r) {
        return r.json();
      })
      .then(function (d) {
        if (d.error) {
          openModal("Tag detail", tagID, '<p class="text-neg-400">' + esc(d.error) + "</p>");
          return;
        }
        var body =
          '<h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">' +
          "Users (" +
          (d.user_count || 0) +
          ")</h3>" +
          userRows(d.users);

        body +=
          '<h3 class="mb-2 mt-5 text-xs font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">' +
          "Grants access to (" +
          (d.item_count || 0) +
          ")</h3>";
        var groups = d.groups || [];
        if (!groups.length) {
          body += '<p class="text-black-700 dark:text-black-600">Nothing yet.</p>';
        } else {
          groups.forEach(function (g) {
            body +=
              '<p class="mt-3 text-xs font-medium text-black-900 dark:text-white-100">' +
              esc(g.kind) +
              " (" +
              (g.items || []).length +
              ")</p>" +
              '<ul class="mt-1 space-y-1">';
            (g.items || []).forEach(function (it) {
              body +=
                '<li data-modal-row="' +
                esc(((it.path || "") + " " + (it.label || "") + " " + g.kind).toLowerCase()) +
                '" class="font-mono text-[11px] text-black-800 dark:text-black-600">' +
                esc(it.path) +
                "</li>";
            });
            body += "</ul>";
          });
        }
        var rowCount = (d.users || []).length + (d.item_count || 0);
        openModal(
          "Tag: " + (d.tag_name || ""),
          d.tag_id || tagID,
          modalSearch(rowCount, "users and items") + body
        );
      })
      .catch(function (e) {
        openModal("Tag detail", tagID, '<p class="text-neg-400">' + esc(e.message) + "</p>");
      });
  }

  // ── Search ───────────────────────────────────────────────────────────────
  //
  // Rows opt in with data-search="<lowercased name id tags>". An exact id/path
  // match wins outright: typing (or pasting) an id shows that one row, which is
  // the whole point of "kalau match id nya langsung muncul aja".

  function rowsFor(input) {
    var scope = input.closest("main") || document;
    return Array.prototype.slice.call(scope.querySelectorAll("[data-search]"));
  }

  function applyFilter(input) {
    var q = input.value.trim().toLowerCase();
    var rows = rowsFor(input);
    var shown = 0;
    var exact = null;

    if (q) {
      for (var i = 0; i < rows.length; i++) {
        var ids = (rows[i].getAttribute("data-search-id") || "").toLowerCase().split(" ");
        if (ids.indexOf(q) !== -1) {
          exact = rows[i];
          break;
        }
      }
    }

    var visible = [];
    rows.forEach(function (row) {
      var hay = (row.getAttribute("data-search") || "").toLowerCase();
      var match = !q || (exact ? row === exact : hay.indexOf(q) !== -1);
      row.classList.toggle("hidden", !match);
      if (match) {
        shown++;
        visible.push(row);
      }
    });

    // Keep parent/child rows together: a connected account is meaningless
    // without the instance above it, and searching an instance should still
    // show the accounts under it.
    if (q) {
      visible.forEach(function (row) {
        var parentID = row.getAttribute("data-search-parent");
        if (parentID) {
          var parent = document.getElementById(parentID);
          if (parent) parent.classList.remove("hidden");
        }
      });
      visible.forEach(function (row) {
        if (!row.id) return;
        rows.forEach(function (child) {
          if (child.getAttribute("data-search-parent") === row.id) {
            child.classList.remove("hidden");
          }
        });
      });
    }

    var out = (input.closest("main") || document).querySelector(".access-search-count");
    if (out) {
      var label = input.getAttribute("data-count-label") || "items";
      out.textContent = q ? shown + " of " + rows.length + " " + label : rows.length + " " + label;
    }
    if (exact) exact.scrollIntoView({ block: "center" });
  }

  // ── Wiring ───────────────────────────────────────────────────────────────

  document.addEventListener("click", function (e) {
    var badge = e.target.closest && e.target.closest(".access-badge");
    if (badge) {
      e.preventDefault();
      showAccess(badge.getAttribute("data-access-path"));
      return;
    }
    var info = e.target.closest && e.target.closest(".tag-usage-info");
    if (info) {
      e.preventDefault();
      showTagUsage(info.getAttribute("data-tag-id"));
      return;
    }
    if (e.target.id === "access-modal" || e.target.id === "access-modal-close") {
      closeModal();
    }
  });

  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") closeModal();
  });

  document.addEventListener("input", function (e) {
    if (!e.target.classList) return;
    if (e.target.classList.contains("access-search")) {
      applyFilter(e.target);
      return;
    }
    if (e.target.classList.contains("access-modal-search")) {
      filterModal(e.target);
    }
  });

  document.addEventListener("DOMContentLoaded", function () {
    document.querySelectorAll(".access-search").forEach(function (input) {
      applyFilter(input);
    });
  });
})();
