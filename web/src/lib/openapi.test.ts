import { describe, expect, it } from 'vitest'
import { curlExample, endpointGroups, exampleValue, javascriptExample, objectFields, pythonExample, typeLabel, type OpenAPIDoc } from './openapi'

const doc: OpenAPIDoc = {
  tags: [{ name: 'Projects' }, { name: 'Jobs' }],
  paths: {
    '/projects/{projectID}': {
      parameters: [{ $ref: '#/components/parameters/ProjectID' } as never],
      get: {
        operationId: 'getProject',
        summary: 'Get a project',
        tags: ['Projects'],
        'x-roles': ['owner', 'admin'],
        responses: {
          '200': {
            description: 'The project.',
            content: { 'application/json': { schema: { type: 'object', properties: { data: { $ref: '#/components/schemas/Project' } } } } },
          },
          '404': { $ref: '#/components/responses/NotFound' },
        },
      },
      delete: {
        operationId: 'deleteProject',
        tags: ['Projects'],
        responses: { '204': { description: 'Deleted.' } },
      },
    },
    '/projects': {
      get: {
        operationId: 'listProjects',
        tags: ['Projects'],
        parameters: [{ name: 'limit', in: 'query', schema: { type: 'integer' } }],
        responses: {
          '200': {
            description: 'A page.',
            content: {
              'application/json': {
                schema: {
                  allOf: [
                    { type: 'object', required: ['has_more'], properties: { has_more: { type: 'boolean' } } },
                    { type: 'object', properties: { data: { type: 'array', items: { $ref: '#/components/schemas/Project' } } } },
                  ],
                },
              },
            },
          },
        },
      },
      post: {
        operationId: 'createProject',
        tags: ['Projects'],
        requestBody: { content: { 'application/json': { schema: { $ref: '#/components/schemas/Project' } } } },
        responses: {},
      },
    },
  },
  components: {
    parameters: { ProjectID: { name: 'projectID', in: 'path', required: true, schema: { type: 'integer', format: 'int64' } } },
    responses: { NotFound: { description: 'Not in your organization.' } },
    schemas: {
      Status: { type: 'string', enum: ['draft', 'in_progress'] },
      Project: {
        type: 'object',
        required: ['id', 'status'],
        properties: {
          id: { type: 'integer', format: 'int64' },
          status: { $ref: '#/components/schemas/Status' },
          deadline: { type: 'string', format: 'date-time', nullable: true, description: 'When it is due.' },
          targets: { type: 'array', minItems: 1, items: { type: 'string', example: 'de' } },
          owner: { $ref: '#/components/schemas/User' },
        },
      },
      User: { type: 'object', properties: { id: { type: 'integer' }, email: { type: 'string' } } },
    },
  },
}

describe('typeLabel', () => {
  it('names objects by their ref and labels scalars, enums and arrays', () => {
    expect(typeLabel(doc, { $ref: '#/components/schemas/Project' })).toBe('Project')
    expect(typeLabel(doc, { $ref: '#/components/schemas/Status' })).toBe('enum<string>')
    expect(typeLabel(doc, { type: 'string', format: 'date-time', nullable: true })).toBe('string<date-time>')
    expect(typeLabel(doc, { type: 'integer', format: 'int64' })).toBe('integer')
    expect(typeLabel(doc, { type: 'array', items: { $ref: '#/components/schemas/Project' } })).toBe('Project[]')
    expect(typeLabel(doc, { allOf: [{ $ref: '#/components/schemas/Project' }], nullable: true })).toBe('Project')
  })
})

describe('objectFields', () => {
  it('lists properties with required, nullable, allowed values and limits', () => {
    const fields = objectFields(doc, { $ref: '#/components/schemas/Project' })
    expect(fields.map(f => [f.name, f.type, f.required])).toEqual([
      ['id', 'integer', true],
      ['status', 'enum<string>', true],
      ['deadline', 'string<date-time>', false],
      ['targets', 'string[]', false],
      ['owner', 'User', false],
    ])
    expect(fields[1].enumValues).toEqual(['draft', 'in_progress'])
    expect(fields[2]).toMatchObject({ nullable: true, description: 'When it is due.' })
    expect(fields[3].constraints).toEqual(['At least 1 item'])
  })

  it('nests the fields of an object property', () => {
    const owner = objectFields(doc, { $ref: '#/components/schemas/Project' }).find(f => f.name === 'owner')!
    expect(owner.children.map(c => c.name)).toEqual(['id', 'email'])
  })
})

describe('exampleValue', () => {
  it('uses examples, then the first enum value, then placeholders by type', () => {
    expect(exampleValue(doc, { $ref: '#/components/schemas/Project' })).toEqual({
      id: 123,
      status: 'draft',
      deadline: '2026-01-01T09:00:00Z',
      targets: ['de'],
      owner: { id: 123, email: '<string>' },
    })
  })
})

describe('endpointGroups', () => {
  const groups = endpointGroups(doc)
  const byId = Object.fromEntries(groups.flatMap(g => g.endpoints).map(e => [e.id, e]))

  it('groups by tag in the spec order and drops empty tags', () => {
    expect(groups.map(g => g.tag)).toEqual(['Projects'])
    expect(Object.keys(byId).sort()).toEqual(['createProject', 'deleteProject', 'getProject', 'listProjects'])
  })

  it('lists a tag in x-order, not path order', () => {
    const ordered: OpenAPIDoc = {
      tags: [{ name: 'T' }],
      paths: {
        '/a': { get: { operationId: 'second', tags: ['T'], 'x-order': 2 } },
        '/b': { get: { operationId: 'first', tags: ['T'], 'x-order': 1 } },
      },
    }
    expect(endpointGroups(ordered)[0].endpoints.map(e => e.id)).toEqual(['first', 'second'])
  })

  it('leaves out x-hidden operations, and a tag left empty by them', () => {
    const hidden: OpenAPIDoc = {
      tags: [{ name: 'T' }, { name: 'Spec' }],
      paths: {
        '/a': { get: { operationId: 'shown', tags: ['T'] } },
        '/openapi.json': { get: { operationId: 'getOpenAPISpec', tags: ['Spec'], 'x-hidden': true } },
      },
    }
    expect(endpointGroups(hidden).map(g => g.tag)).toEqual(['T'])
    expect(endpointGroups(hidden)[0].endpoints.map(e => e.id)).toEqual(['shown'])
  })

  it('includes path-level parameters, resolved', () => {
    expect(byId.getProject.parameters).toMatchObject([{ name: 'projectID', location: 'path', type: 'integer', required: true }])
    expect(byId.getProject.roles).toEqual(['owner', 'admin'])
  })

  it('reads the success response, merging allOf', () => {
    expect(byId.getProject.result?.status).toBe('200')
    expect(byId.getProject.result?.fields.map(f => f.name)).toEqual(['data'])
    const list = byId.listProjects.result!
    // Required fields come first.
    expect(list.fields.map(f => [f.name, f.type])).toEqual([['has_more', 'boolean'], ['data', 'Project[]']])
    expect(list.fields[1].children.map(c => c.name)).toContain('deadline')
    expect(byId.deleteProject.result).toBeNull()
  })

  it('resolves shared responses and request bodies', () => {
    expect(byId.getProject.responses.map(r => [r.status, r.description])).toEqual([
      ['200', 'The project.'],
      ['404', 'Not in your organization.'],
    ])
    expect(byId.getProject.responses[0].example).toMatchObject({ data: { id: 123, status: 'draft' } })
    // The shared 404 has no body schema here, so no sample.
    expect(byId.getProject.responses[1].example).toBeNull()
    expect(byId.createProject.body?.contentType).toBe('application/json')
    expect(byId.createProject.body?.fields.length).toBe(5)
    expect(byId.getProject.body).toBeNull()
  })
})

describe('curlExample', () => {
  const byId = Object.fromEntries(endpointGroups(doc).flatMap(g => g.endpoints).map(e => [e.id, e]))

  it('builds a request with the key header and the example body', () => {
    const cmd = curlExample(byId.createProject, 'https://app.example.com')
    expect(cmd).toContain("--request POST")
    expect(cmd).toContain("--url 'https://app.example.com/api/v1/projects'")
    expect(cmd).toContain('Authorization: Bearer $OPENLOC_KEY')
    expect(cmd).toContain('"status": "draft"')
  })

  it('has no body for a read', () => {
    const cmd = curlExample(byId.getProject, 'https://app.example.com')
    expect(cmd).toContain("/api/v1/projects/{projectID}'")
    expect(cmd).not.toContain('--data')
  })
})

describe('code samples', () => {
  const byId = Object.fromEntries(endpointGroups(doc).flatMap(g => g.endpoints).map(e => [e.id, e]))

  it('reads the key from the environment in every language', () => {
    for (const make of [curlExample, javascriptExample, pythonExample]) {
      const code = make(byId.createProject, 'https://app.example.com')
      expect(code).toContain('OPENLOC_KEY')
      expect(code).toContain('https://app.example.com/api/v1/projects')
    }
  })

  it('sends the example body as JSON', () => {
    expect(javascriptExample(byId.createProject, 'https://x')).toContain("method: 'POST'")
    expect(javascriptExample(byId.createProject, 'https://x')).toContain('"status": "draft"')
    const py = pythonExample(byId.createProject, 'https://x')
    expect(py).toContain('requests.post(')
    expect(py).toContain('json={')
  })

  it('reads data only when the response has a body', () => {
    expect(javascriptExample(byId.getProject, 'https://x')).toContain('const { data } = await res.json()')
    expect(javascriptExample(byId.deleteProject, 'https://x')).toContain('if (!res.ok)')
    expect(pythonExample(byId.deleteProject, 'https://x')).not.toContain('res.json()')
  })
})

describe('error examples', () => {
  it('use the status code and the description as the message', () => {
    const withErrors: OpenAPIDoc = {
      paths: {
        '/x': {
          get: {
            operationId: 'x',
            responses: {
              '403': { $ref: '#/components/responses/Forbidden' },
              '429': { description: 'Too many requests (`rate_limited`).', content: { 'application/json': { schema: { $ref: '#/components/schemas/Error' } } } },
            },
          },
        },
      },
      components: {
        schemas: { Error: { type: 'object', properties: { error: { type: 'object' } } } },
        responses: { Forbidden: { description: 'Not your role.', content: { 'application/json': { schema: { $ref: '#/components/schemas/Error' } } } } },
      },
    }
    const [forbidden, limited] = endpointGroups(withErrors)[0].endpoints[0].responses
    expect(forbidden.example).toEqual({ error: { code: 'Forbidden', message: 'Not your role.' } })
    expect(limited.example).toEqual({ error: { code: 'rate_limited', message: 'Too many requests (rate_limited).' } })
  })
})
