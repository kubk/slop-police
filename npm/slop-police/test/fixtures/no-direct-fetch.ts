import { defineRule, isIdentifier } from 'slop-police'

export default defineRule({
  name: 'no-direct-fetch',
  options: { allowIn: '' },
  visit: {
    CallExpression(node, ctx) {
      if (ctx.options.allowIn && ctx.file.path.startsWith(ctx.options.allowIn)) return
      if (isIdentifier(node.expression) && node.expression.text === 'fetch') {
        ctx.report(node.expression, 'Use the API client instead of fetch()')
      }
    },
  },
})

export const helper = 1
