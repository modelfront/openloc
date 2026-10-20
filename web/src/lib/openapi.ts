// Reads the customer API's OpenAPI document (GET /api/v1/openapi.json) into
// the shapes the endpoint pages render. Only the subset of OpenAPI 3.0 the
// spec in internal/publicapi/openapi uses is supported: local $refs, objects,
// arrays, allOf and nullable.

export type Schema = {
  $ref?: string
  type?: string
  format?: string
  description?: string
  enum?: unknown[]
  nullable?: boolean
  required?: string[]
  properties?: Record<string, Schema>
  items?: Schema
  allOf?: Schema[]
  example?: unknown
  default?: unknown
  minimum?: number
  maximum?: number
  maxLength?: number
  minItems?: number
}

type Parameter = {
  $ref?: string
  name: string
  in: string
  required?: boolean
  description?: string
  schema?: Schema
}

type MediaType = { schema?: Schema }

type Response = {
  $ref?: string
  description?: string
  content?: Record<string, MediaType>
}

type Operation = {
  operationId: string
  summary?: string
  description?: string
  tags?: string[]
  parameters?: Parameter[]
  requestBody?: { content?: Record<string, MediaType> }
  responses?: Record<string, Response>
  'x-roles'?: string[]
  /** Position in the spec files as written; the reference lists operations in this order. */
  'x-order'?: number
  /** Kept out of the reference: documented for clients, not listed for readers. */
  'x-hidden'?: boolean
}

type PathItem = Record<string, Operation | Parameter[] | undefined> & { parameters?: Parameter[] }

export type OpenAPIDoc = {
  tags?: { name: string }[]
  paths: Record<string, PathItem>
  components?: {
    schemas?: Record<string, Schema>
    parameters?: Record<string, Parameter>
    responses?: Record<string, Response>
  }
}

/** One documented field: a parameter, a body field or a response field. */
export type Field = {
  name: string
  /** path / query / header — parameters only. */
  location?: string
  type: string
  required: boolean
  nullable: boolean
  description: string
  /** Allowed values of an enum. */
  enumValues: string[]
  defaultValue?: string
  /** Limits, as short phrases: "Maximum: 100". */
  constraints: string[]
  /** The fields of a nested object, or of each item of an array of objects. */
  children: Field[]
}

export type EndpointResponse = {
  status: string
  description: string
  /** A sample body for a JSON response; null for a file or an empty body. */
  example: unknown
}

export type Endpoint = {
  id: string
  tag: string
  method: string
  path: string
  summary: string
  description: string
  roles: string[]
  parameters: Field[]
  /** Request body; null when the endpoint takes none. */
  body: { contentType: string; fields: Field[]; example: unknown } | null
  /** The success response, when it has a JSON body. */
  result: { status: string; fields: Field[]; example: unknown } | null
  /** The success response of an endpoint that returns a file or nothing. */
  resultType: string | null
  responses: EndpointResponse[]
}

export type EndpointGroup = { tag: string; endpoints: Endpoint[] }

const METHODS = ['get', 'post', 'put', 'patch', 'delete'] as const

/** Follows a local `#/components/<kind>/<name>` reference. */
function deref<T extends { $ref?: string }>(doc: OpenAPIDoc, value: T): T {
  if (!value.$ref) return value
  const [, , kind, name] = value.$ref.split('/')
  const table = doc.components?.[kind as keyof NonNullable<OpenAPIDoc['components']>] as Record<string, T> | undefined
  const target = table?.[name]
  return target ? deref(doc, target) : value
}

const refName = (ref: string) => ref.split('/').pop() ?? ref

/** A schema with allOf merged into one: properties, required, nullable. */
function flatten(doc: OpenAPIDoc, schema: Schema): Schema {
  const s = deref(doc, schema)
  if (!s.allOf) return s
  const out: Schema = { ...s, allOf: undefined, properties: { ...s.properties }, required: [...(s.required ?? [])] }
  for (const part of s.allOf) {
    const p = flatten(doc, part)
    out.type ??= p.type
    out.format ??= p.format
    out.enum ??= p.enum
    out.items ??= p.items
    out.description ??= p.description
    Object.assign(out.properties!, p.properties)
    out.required!.push(...(p.required ?? []))
  }
  if (Object.keys(out.properties!).length === 0) delete out.properties
  return out
}

const isObject = (s: Schema) => s.type === 'object' || !!s.properties

/** A type label in the style of most API references: `string<date-time>`, `Project[]`, `enum<string>`. */
export function typeLabel(doc: OpenAPIDoc, schema: Schema | undefined): string {
  if (!schema) return ''
  const s = flatten(doc, schema)
  const named = schema.$ref ?? (schema.allOf?.length === 1 ? schema.allOf[0].$ref : undefined)
  if (named && isObject(s)) return refName(named)
  if (s.type === 'array') return `${typeLabel(doc, s.items) || 'any'}[]`
  if (s.enum) return `enum<${s.type ?? 'string'}>`
  if (s.format && s.format !== 'int64') return `${s.type}<${s.format}>`
  return s.type ?? 'object'
}

function constraintsOf(s: Schema): string[] {
  const out: string[] = []
  if (s.minimum !== undefined) out.push(`Minimum: ${s.minimum}`)
  if (s.maximum !== undefined) out.push(`Maximum: ${s.maximum}`)
  if (s.maxLength !== undefined) out.push(`Maximum length: ${s.maxLength}`)
  if (s.minItems !== undefined) out.push(`At least ${s.minItems} item${s.minItems === 1 ? '' : 's'}`)
  return out
}

function fieldOf(doc: OpenAPIDoc, name: string, prop: Schema, required: boolean, depth: number): Field {
  const s = flatten(doc, prop)
  const items = s.type === 'array' && s.items ? flatten(doc, s.items) : null
  const nested = isObject(s) ? prop : items && isObject(items) ? s.items! : null
  return {
    name,
    type: typeLabel(doc, prop),
    required,
    nullable: !!(prop.nullable || s.nullable),
    description: prop.description ?? s.description ?? '',
    enumValues: (s.enum ?? items?.enum ?? []).map(String),
    defaultValue: s.default === undefined ? undefined : JSON.stringify(s.default),
    constraints: constraintsOf(s),
    children: nested && depth < 4 ? objectFields(doc, nested, depth + 1) : [],
  }
}

/** The fields of an object schema, one per property, in declaration order. */
export function objectFields(doc: OpenAPIDoc, schema: Schema, depth = 0): Field[] {
  const s = flatten(doc, schema)
  const required = s.required ?? []
  const fields = Object.entries(s.properties ?? {}).map(([name, prop]) => fieldOf(doc, name, prop, required.includes(name), depth))
  // Required first; otherwise the spec's order. sort is stable.
  return fields.sort((a, b) => Number(b.required) - Number(a.required))
}

/** A plausible example value for a schema: its `example`, else a placeholder of its type. */
export function exampleValue(doc: OpenAPIDoc, schema: Schema, depth = 0): unknown {
  const s = flatten(doc, schema)
  if (s.example !== undefined) return s.example
  if (s.default !== undefined) return s.default
  if (s.enum?.length) return s.enum[0]
  if (depth > 5) return null
  if (s.type === 'array') return s.items ? [exampleValue(doc, s.items, depth + 1)] : []
  if (isObject(s)) {
    return Object.fromEntries(Object.entries(s.properties ?? {}).map(([k, v]) => [k, exampleValue(doc, v, depth + 1)]))
  }
  switch (s.type) {
    case 'integer':
      return 123
    case 'number':
      return 1.5
    case 'boolean':
      return true
    case 'string':
      if (s.format === 'date-time') return '2026-01-01T09:00:00Z'
      if (s.format === 'binary') return '<file>'
      return '<string>'
  }
  return null
}

function parameterFields(doc: OpenAPIDoc, params: Parameter[]): Field[] {
  return params.map(raw => {
    const p = deref(doc, raw)
    const s = p.schema ? flatten(doc, p.schema) : {}
    return {
      ...fieldOf(doc, p.name, p.schema ?? {}, !!p.required, 0),
      location: p.in,
      description: p.description ?? s.description ?? '',
    }
  })
}

function bodyOf(doc: OpenAPIDoc, op: Operation): Endpoint['body'] {
  const content = op.requestBody?.content
  if (!content) return null
  const [contentType, media] = Object.entries(content)[0]
  if (!media.schema) return { contentType, fields: [], example: null }
  return { contentType, fields: objectFields(doc, media.schema), example: exampleValue(doc, media.schema) }
}

function successOf(op: Operation): [string, Response] | undefined {
  return Object.entries(op.responses ?? {}).find(([code]) => code.startsWith('2'))
}

function resultOf(doc: OpenAPIDoc, op: Operation): Endpoint['result'] {
  const ok = successOf(op)
  const schema = ok && deref(doc, ok[1]).content?.['application/json']?.schema
  if (!ok || !schema) return null
  return { status: ok[0], fields: objectFields(doc, schema), example: exampleValue(doc, schema) }
}

function resultTypeOf(doc: OpenAPIDoc, op: Operation): string | null {
  const ok = successOf(op)
  if (!ok) return null
  const content = deref(doc, ok[1]).content
  if (!content) return null
  const types = Object.keys(content).filter(t => t !== 'application/json')
  return types.length ? types.join(' or ') : null
}

// The `code` an error body carries for each status: the server's machine codes
// where it sets one, otherwise the HTTP status text it falls back to.
const ERROR_CODES: Record<string, string> = {
  '400': 'Bad Request',
  '401': 'Unauthorized',
  '403': 'Forbidden',
  '404': 'Not Found',
  '409': 'Conflict',
  '413': 'Request Entity Too Large',
  '429': 'rate_limited',
}

function responseOf(doc: OpenAPIDoc, status: string, raw: Response): EndpointResponse {
  const r = deref(doc, raw)
  const description = r.description ?? ''
  const schema = r.content?.['application/json']?.schema
  let example: unknown = schema ? exampleValue(doc, schema) : null
  if (!status.startsWith('2') && schema) {
    // A readable error instead of placeholders: the code, and the
    // description as the message.
    example = { error: { code: ERROR_CODES[status] ?? 'error', message: description.replace(/`/g, '') } }
  }
  return { status, description, example }
}

/** Every operation not marked x-hidden, grouped by its tag in the spec's tag order. */
export function endpointGroups(doc: OpenAPIDoc): EndpointGroup[] {
  const groups = new Map<string, (Endpoint & { order: number })[]>((doc.tags ?? []).map(t => [t.name, []]))
  for (const [path, item] of Object.entries(doc.paths)) {
    const shared = item.parameters ?? []
    for (const method of METHODS) {
      const op = item[method] as Operation | undefined
      if (!op || op['x-hidden']) continue
      const tag = op.tags?.[0] ?? 'Other'
      if (!groups.has(tag)) groups.set(tag, [])
      groups.get(tag)!.push({
        order: op['x-order'] ?? Number.MAX_SAFE_INTEGER,
        id: op.operationId,
        tag,
        method: method.toUpperCase(),
        path,
        summary: op.summary ?? '',
        description: op.description ?? '',
        roles: op['x-roles'] ?? [],
        parameters: parameterFields(doc, [...shared, ...(op.parameters ?? [])]),
        body: bodyOf(doc, op),
        result: resultOf(doc, op),
        resultType: resultTypeOf(doc, op),
        responses: Object.entries(op.responses ?? {}).map(([status, r]) => responseOf(doc, status, r)),
      })
    }
  }
  return [...groups]
    .filter(([, eps]) => eps.length > 0)
    .map(([tag, eps]) => ({ tag, endpoints: eps.sort((a, b) => a.order - b.order).map(({ order, ...e }) => e) }))
}

const ENV_KEY = 'OPENLOC_KEY'

function urlOf(endpoint: Endpoint, baseURL: string): string {
  const query = endpoint.parameters.filter(p => p.location === 'query' && p.required)
  const qs = query.length ? '?' + query.map(p => `${p.name}=<${p.name}>`).join('&') : ''
  return `${baseURL}/api/v1${endpoint.path}${qs}`
}

const isMultipart = (e: Endpoint) => e.body?.contentType === 'multipart/form-data'
const fileFields = (e: Endpoint) => e.body?.fields.filter(f => f.type === 'string<binary>') ?? []
const formFields = (e: Endpoint) => e.body?.fields.filter(f => f.type !== 'string<binary>' && f.required) ?? []

/** A cURL command for an endpoint, with its example body. */
export function curlExample(endpoint: Endpoint, baseURL: string): string {
  const lines = [`curl --request ${endpoint.method} \\`, `  --url '${urlOf(endpoint, baseURL)}' \\`, `  --header "Authorization: Bearer $${ENV_KEY}"`]
  const more = (line: string) => {
    lines[lines.length - 1] += ' \\'
    lines.push(line)
  }
  if (isMultipart(endpoint)) {
    for (const f of fileFields(endpoint)) more(`  --form '${f.name}=@document.docx'`)
    for (const f of formFields(endpoint)) more(`  --form '${f.name}=${String((endpoint.body!.example as Record<string, unknown>)[f.name])}'`)
  } else if (endpoint.body) {
    more(`  --header 'Content-Type: ${endpoint.body.contentType}'`)
    more(`  --data '${JSON.stringify(endpoint.body.example, null, 2)}'`)
  }
  return lines.join('\n')
}

/** The same request with fetch, for Node 18+ or a server-side script. */
export function javascriptExample(endpoint: Endpoint, baseURL: string): string {
  const headers = [`    Authorization: \`Bearer \${process.env.${ENV_KEY}}\`,`]
  const lines: string[] = []
  let body = ''
  if (isMultipart(endpoint)) {
    lines.push('const form = new FormData()')
    for (const f of fileFields(endpoint)) lines.push(`form.append('${f.name}', new Blob([fs.readFileSync('document.docx')]), 'document.docx')`)
    lines.push('')
    body = '  body: form,\n'
  } else if (endpoint.body) {
    headers.push(`    'Content-Type': '${endpoint.body.contentType}',`)
    body = `  body: JSON.stringify(${JSON.stringify(endpoint.body.example, null, 2).replace(/\n/g, '\n  ')}),\n`
  }
  lines.push(
    `const res = await fetch('${urlOf(endpoint, baseURL)}', {`,
    `  method: '${endpoint.method}',`,
    '  headers: {',
    ...headers,
    '  },',
    ...(body ? [body.replace(/\n$/, '')] : []),
    '})',
    endpoint.result ? 'const { data } = await res.json()' : 'if (!res.ok) throw new Error(await res.text())',
  )
  if (isMultipart(endpoint)) lines.unshift("import fs from 'node:fs'", '')
  return lines.join('\n')
}

const pyLiteral = (v: unknown): string =>
  JSON.stringify(v, null, 4)
    .replace(/\btrue\b/g, 'True')
    .replace(/\bfalse\b/g, 'False')
    .replace(/\bnull\b/g, 'None')

/** The same request with the requests library. */
export function pythonExample(endpoint: Endpoint, baseURL: string): string {
  const args = [`    "${urlOf(endpoint, baseURL)}",`, `    headers={"Authorization": f"Bearer {os.environ['${ENV_KEY}']}"},`]
  if (isMultipart(endpoint)) {
    for (const f of fileFields(endpoint)) args.push(`    files={"${f.name}": open("document.docx", "rb")},`)
  } else if (endpoint.body) {
    args.push(`    json=${pyLiteral(endpoint.body.example).replace(/\n/g, '\n    ')},`)
  }
  return [
    'import os',
    'import requests',
    '',
    `res = requests.${endpoint.method.toLowerCase()}(`,
    ...args,
    ')',
    'res.raise_for_status()',
    ...(endpoint.result ? ['data = res.json()["data"]'] : []),
  ].join('\n')
}
