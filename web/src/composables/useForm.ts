/**
 * Schema-bound form composable. Pairs a Zod schema with reactive `values`,
 * typed `errors`, and a `submit()` wrapper that runs validation first.
 */

import { reactive, ref, type Ref } from 'vue'
import type { ZodType } from 'zod'
import { normalizeError } from '@/lib/errors'

export interface UseFormOptions<TInput extends object, TOutput> {
    schema: ZodType<TOutput, TInput>
    initialValues: TInput
    onSubmit: (values: TOutput) => Promise<void> | void
    onInvalid?: (errors: Record<string, string>) => void
}

export interface UseFormReturn<TInput extends object, TOutput> {
    values: TInput
    errors: Record<string, string>
    submitting: Ref<boolean>
    serverError: Ref<string | null>
    submit: () => Promise<void>
    validateField: (path: keyof TInput & string) => boolean
    validate: () => TOutput | null
    clearError: (path: keyof TInput & string) => void
    reset: () => void
}

export function useForm<TInput extends object, TOutput>(
    options: UseFormOptions<TInput, TOutput>,
): UseFormReturn<TInput, TOutput> {
    const { schema, initialValues, onSubmit, onInvalid } = options

    const values = reactive({ ...initialValues }) as TInput
    const errors = reactive<Record<string, string>>({})
    const submitting = ref(false)
    const serverError = ref<string | null>(null)

    function clearAll() {
        for (const k of Object.keys(errors)) delete errors[k]
        serverError.value = null
    }

    function validate(): TOutput | null {
        clearAll()
        const result = schema.safeParse(values)
        if (result.success) return result.data
        for (const issue of result.error.issues) {
            const path = issue.path.join('.')
            if (!errors[path]) errors[path] = issue.message
        }
        onInvalid?.(errors)
        return null
    }

    function validateField(path: keyof TInput & string): boolean {
        const result = schema.safeParse(values)
        delete errors[path]
        if (!result.success) {
            for (const issue of result.error.issues) {
                if (issue.path.join('.') === path) {
                    errors[path] = issue.message
                    return false
                }
            }
        }
        return true
    }

    async function submit(): Promise<void> {
        const parsed = validate()
        if (!parsed) return
        submitting.value = true
        try {
            await onSubmit(parsed)
        } catch (err) {
            serverError.value = normalizeError(err).message
            throw err
        } finally {
            submitting.value = false
        }
    }

    function clearError(path: keyof TInput & string) {
        delete errors[path]
    }

    function reset() {
        Object.assign(values as object, initialValues)
        clearAll()
    }

    return {
        values, errors, submitting, serverError,
        submit, validate, validateField, clearError, reset,
    }
}
