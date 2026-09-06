/**
 * mise-lib-template — sample library module.
 * Rename and replace with your actual implementation.
 */

export interface GreetOptions {
  /** Name to greet */
  name: string;
}

/**
 * Returns a greeting message.
 *
 * @example
 * ```ts
 * import { greet } from 'mise-lib-template';
 * console.log(greet({ name: 'world' })); // "Hello, world!"
 * ```
 */
export function greet(options: GreetOptions): string {
  return `Hello, ${options.name}!`;
}
