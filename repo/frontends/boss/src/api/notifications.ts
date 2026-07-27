import { coreApi } from './coreClient'
import type { components } from './core-schema'

// ─── Types ───────────────────────────────────────────────────────────

export type EmailSmtpConfigResponse = components['schemas']['EmailSmtpConfigResponse']
export type PutEmailSmtpConfigRequest = components['schemas']['PutEmailSmtpConfigRequest']
export type EmailRecipient = components['schemas']['EmailRecipient']
export type EmailRecipientListResponse = components['schemas']['EmailRecipientListResponse']
export type CreateEmailRecipientRequest = components['schemas']['CreateEmailRecipientRequest']
export type UpdateEmailRecipientRequest = components['schemas']['UpdateEmailRecipientRequest']
export type EmailSubscription = components['schemas']['EmailSubscription']
export type EmailSubscriptionListResponse = components['schemas']['EmailSubscriptionListResponse']
export type PutEmailSubscriptionsRequest = components['schemas']['PutEmailSubscriptionsRequest']
export type SendTestEmailResponse = components['schemas']['SendTestEmailResponse']

// ─── SMTP Config ─────────────────────────────────────────────────────

export async function getEmailSmtpConfig(): Promise<EmailSmtpConfigResponse> {
  const { data, error, response } = await coreApi.GET('/notifications/email/smtp')
  if (error) {
    const err = error as { code?: string; message?: string }
    throw { code: err.code, message: err.message, status: response.status }
  }
  return data as EmailSmtpConfigResponse
}

export async function putEmailSmtpConfig(
  body: PutEmailSmtpConfigRequest,
): Promise<EmailSmtpConfigResponse> {
  const { data, error, response } = await coreApi.PUT('/notifications/email/smtp', {
    params: { header: { 'Idempotency-Key': crypto.randomUUID() } },
    body,
  })
  if (error) {
    const err = error as { code?: string; message?: string }
    throw { code: err.code, message: err.message, status: response.status }
  }
  return data as EmailSmtpConfigResponse
}

// ─── Recipients ──────────────────────────────────────────────────────

export async function listEmailRecipients(): Promise<EmailRecipientListResponse> {
  const { data, error, response } = await coreApi.GET('/notifications/email/recipients')
  if (error) {
    const err = error as { code?: string; message?: string }
    throw { code: err.code, message: err.message, status: response.status }
  }
  return data as EmailRecipientListResponse
}

export async function createEmailRecipient(
  body: CreateEmailRecipientRequest,
): Promise<EmailRecipient> {
  const { data, error, response } = await coreApi.POST('/notifications/email/recipients', {
    params: { header: { 'Idempotency-Key': crypto.randomUUID() } },
    body,
  })
  if (error) {
    const err = error as { code?: string; message?: string }
    throw { code: err.code, message: err.message, status: response.status }
  }
  return data as EmailRecipient
}

export async function updateEmailRecipient(
  recipientId: string,
  body: UpdateEmailRecipientRequest,
): Promise<EmailRecipient> {
  const { data, error, response } = await coreApi.PATCH(
    '/notifications/email/recipients/{recipient_id}',
    {
      params: {
        header: { 'Idempotency-Key': crypto.randomUUID() },
        path: { recipient_id: recipientId },
      },
      body,
    },
  )
  if (error) {
    const err = error as { code?: string; message?: string }
    throw { code: err.code, message: err.message, status: response.status }
  }
  return data as EmailRecipient
}

export async function deleteEmailRecipient(recipientId: string): Promise<void> {
  const { error, response } = await coreApi.DELETE(
    '/notifications/email/recipients/{recipient_id}',
    {
      params: {
        header: { 'Idempotency-Key': crypto.randomUUID() },
        path: { recipient_id: recipientId },
      },
    },
  )
  if (error) {
    const err = error as { code?: string; message?: string }
    throw { code: err.code, message: err.message, status: response.status }
  }
}

// ─── Subscriptions ───────────────────────────────────────────────────

export async function listEmailSubscriptions(): Promise<EmailSubscriptionListResponse> {
  const { data, error, response } = await coreApi.GET('/notifications/email/subscriptions')
  if (error) {
    const err = error as { code?: string; message?: string }
    throw { code: err.code, message: err.message, status: response.status }
  }
  return data as EmailSubscriptionListResponse
}

export async function putEmailSubscriptions(
  body: PutEmailSubscriptionsRequest,
): Promise<EmailSubscriptionListResponse> {
  const { data, error, response } = await coreApi.PUT('/notifications/email/subscriptions', {
    params: { header: { 'Idempotency-Key': crypto.randomUUID() } },
    body,
  })
  if (error) {
    const err = error as { code?: string; message?: string }
    throw { code: err.code, message: err.message, status: response.status }
  }
  return data as EmailSubscriptionListResponse
}

// ─── Test Send ────────────────────────────────────────────────────────

export async function sendTestEmail(): Promise<SendTestEmailResponse> {
  const { data, error, response } = await coreApi.POST('/notifications/email/test', {
    params: { header: { 'Idempotency-Key': crypto.randomUUID() } },
  })
  if (error) {
    const err = error as { code?: string; message?: string }
    throw { code: err.code, message: err.message, status: response.status }
  }
  return data as SendTestEmailResponse
}
