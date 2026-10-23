// The printer base (WP-145): maps the generic nodes of model.js to a Doc
// (doc.js) and lays it out. A language's printer (lang/<id>.js) extends it
// and overrides its syntax: the leaf tokens (string escaping, numbers,
// literals, identifiers), declarations, named arguments, the delimiters of
// lists and maps, imports and the program frame. Line breaks come from the
// layout, never from the printers.

import { concat, group, hardline, ifBreak, indent, join, line, print, softline } from './doc.js';

export class Printer {
  width = 80;
  unit = '  ';
  semi = '';
  // A trailing comma in broken argument lists and collections.
  trailingArgs = true;
  trailingItems = true;
  memberSep = '.';
  staticSep = '.';
  // Go writes the dot at the end of a broken chain's line.
  chainDotAtEnd = false;

  // ---- leaf tokens ------------------------------------------------------------

  string(s) { return JSON.stringify(s); }
  number(v) { return v; }
  nullToken() { return 'null'; }
  bool(b) { return b ? 'true' : 'false'; }
  ident(name) { return name; }
  symbol(name) { return `:${name}`; }
  char(c) { return this.string(c); }

  // ---- layout helpers -----------------------------------------------------------

  // bracket lays out items between open and close: on one line when they
  // fit, else one per line, indented (pad puts spaces inside the brackets
  // on one line, as in "{ a: 1 }").
  bracket(open, items, close, { trailing = this.trailingItems, pad = false, sep = ',', force = false } = {}) {
    if (!items.length) return concat(open, close);
    const inner = pad ? line : softline;
    return group(concat(open, indent(concat(inner, join(concat(sep, line), items))), ifBreak(trailing ? sep : ''), inner, close), { shouldBreak: force });
  }

  args(list, { trailing = this.trailingArgs } = {}) {
    if (list.length === 1 && this.hugs(list[0])) return concat('(', this.expr(list[0]), ')');
    return this.bracket('(', list.map((a) => this.expr(a)), ')', { trailing });
  }

  // hugs: a lone argument that brings its own brackets keeps the call's
  // parentheses on its lines ("f({" … "})").
  hugs(arg) {
    return ['map', 'list', 'composite'].includes(arg.k) || (arg.k === 'new' && (arg.init || arg.items));
  }

  // ---- program ------------------------------------------------------------------

  print(program) {
    return print(this.program(program), { width: this.width, unit: this.unit });
  }

  program(p) {
    const parts = [];
    if (p.header.length) parts.push(this.statements(p.header));
    if (p.imports.length) parts.push(this.imports(p.imports));
    parts.push(this.statements(p.body));
    return concat(join(concat(hardline, hardline), parts), hardline);
  }

  imports(list) { return join(hardline, list.map((i) => this.importLine(i))); }
  importLine(i) { return concat('import ', i.module, this.semi); }

  statements(list) { return join(hardline, list.map((s) => this.statement(s))); }

  statement(s) {
    switch (s.k) {
      case 'let': return this.letStmt(s);
      case 'stmt': return concat(this.expr(s.expr), s.noSemi ? '' : this.semi);
      case 'return': return concat('return ', this.expr(s.value), this.semi);
      case 'if': return concat('if ', this.expr(s.cond), ' {', indent(concat(hardline, this.statements(s.body))), hardline, '}');
      case 'func': return concat(this.funcHead(s), ' {', indent(concat(hardline, this.statements(s.body))), hardline, '}');
      case 'class': return concat(this.classHead(s), ' {', indent(concat(hardline, this.statements(s.body))), hardline, '}');
      case 'cmd': return concat(s.keyword, s.args.length ? concat(' ', join(', ', s.args.map((a) => this.expr(a)))) : '', this.semi);
      case 'blank': return '';
      default: throw new Error(`printer: unknown statement ${s.k}`);
    }
  }

  letStmt(s) { return concat('let ', join(', ', s.names.map((n) => this.ident(n))), ' = ', this.expr(s.value), this.semi); }
  funcHead(f) { return concat('function ', f.name, '()'); }
  classHead(c) { return concat('class ', c.name); }

  // ---- expressions --------------------------------------------------------------

  expr(e) {
    const f = this[`x_${e.k}`];
    if (!f) throw new Error(`printer: unknown expression ${e.k}`);
    return f.call(this, e);
  }

  x_lit(e) {
    const { k, v } = e.v;
    switch (k) {
      case 'null': return this.nullToken();
      case 'bool': return this.bool(v);
      case 'int': case 'num': return this.number(v, k);
      case 'char': return this.char(v);
      default: return this.string(v);
    }
  }

  x_id(e) { return this.ident(e.name); }
  x_type(e) { return e.name; }
  x_sym(e) { return this.symbol(e.name); }
  x_named(e) { return concat(e.name, ': ', this.expr(e.value)); }
  x_index(e) { return concat(this.expr(e.obj), '[', this.expr(e.key), ']'); }
  x_unwrap(e) { return concat(this.expr(e.expr), '!'); }
  x_try(e) { return concat(this.expr(e.expr), '?'); }
  x_ref(e) { return concat('&', this.expr(e.expr)); }
  x_await(e) { return concat('await ', this.expr(e.expr)); }
  x_binary(e) { return concat(this.expr(e.left), ` ${e.op} `, this.expr(e.right)); }
  x_new(e) { return concat('new ', e.type, this.args(e.args)); }

  x_list(e) { return this.bracket('[', e.items.map((i) => this.expr(i)), ']'); }
  x_map(e) { return this.bracket('{', e.entries.map((en) => concat(this.expr(en.key), ': ', this.expr(en.value))), '}'); }

  x_block(e) {
    const stmts = e.stmts.map((s) => this.statement(s));
    return group(concat('{', indent(concat(line, join(concat(ifBreak('', ';'), line), stmts))), line, '}'));
  }

  // Members and calls: a chain of calls (a.b(…).c(…)…) is laid out as one
  // group: on one line, or one call per line.
  x_member(e) { return this.chain(e); }
  x_call(e) { return this.chain(e); }

  sep(link) { return link.static ? this.staticSep : this.memberSep; }

  link(l) {
    const parts = [this.memberName(l.name, l)];
    if (l.call) parts.push(this.callArgs(l.call));
    return concat(...parts);
  }

  memberName(name) { return name; }

  callArgs(call) {
    const parts = [];
    if (call.parens === false) {
      if (call.args.length) parts.push(' ', join(', ', call.args.map((a) => this.expr(a))));
    } else if (call.args.length || !call.block) {
      parts.push(this.args(call.args));
    }
    if (call.block) parts.push(' ', this.x_block(call.block));
    return concat(...parts);
  }

  chain(e) {
    // Flatten a.b(…).c(…) into its head and links; static members (a
    // type's) belong to the head.
    const links = [];
    let node = e;
    for (;;) {
      if (node.k === 'call' && node.fn.k === 'member' && !node.fn.static) {
        links.unshift({ name: node.fn.name, call: node });
        node = node.fn.obj;
      } else if (node.k === 'member' && !node.static) {
        links.unshift({ name: node.name });
        node = node.obj;
      } else break;
    }
    let head;
    if (node.k === 'call') head = concat(this.expr(node.fn), this.callArgs(node));
    else if (node.k === 'member') head = concat(this.expr(node.obj), this.staticSep, this.memberName(node.name, node));
    else head = this.expr(node);
    if (!links.length) return head;
    // The first link stays on the head's line after a plain name (img.url(…)).
    let rest = links;
    if (['id', 'type'].includes(node.k) || (node.k === 'member' && node.static)) {
      head = concat(head, this.sep(rest[0]), this.link(rest[0]));
      rest = rest.slice(1);
    }
    if (!rest.length) return head;
    const calls = rest.filter((l) => l.call).length;
    const parts = rest.map((l) => (this.chainDotAtEnd ? concat(this.sep(l), softline, this.link(l)) : concat(softline, this.sep(l), this.link(l))));
    return group(concat(head, indent(concat(...parts))), { shouldBreak: calls >= 3 });
  }
}
