import { isJwtExpired } from 'utils/jwt';
import { StaticStore } from 'common/static-store';
import type { FormProvider, OAuthProvider } from 'common/types';

import { OAUTH_PROVIDERS } from './components/oauth.consts';
import { messages } from './auth.messsages';
import { setItem, getItem } from 'common/local-storage';
import { LS_EMAIL_KEY } from 'common/constants';

/** Providers that handle auth externally and render no UI */
const TRANSPARENT_PROVIDERS: string[] = ['jwt', 'forward'];

export function getProviders(): [OAuthProvider[], FormProvider[]] {
  const oauthProviders: OAuthProvider[] = [];
  const formProviders: FormProvider[] = [];

  StaticStore.config.auth_providers.forEach((p) => {
    if (TRANSPARENT_PROVIDERS.includes(p)) {
      return;
    }
    OAUTH_PROVIDERS.includes(p) ? oauthProviders.push(p as OAuthProvider) : formProviders.push(p as FormProvider);
  });

  return [oauthProviders, formProviders];
}

export function getTokenInvalidReason(token: string): null | keyof typeof messages {
  try {
    if (isJwtExpired(token)) {
      return 'expiredToken';
    }
  } catch (e) {
    return 'invalidToken';
  }

  return null;
}

export function persistEmail(email: string) {
  setItem(LS_EMAIL_KEY, email);
}

export function getPersistedEmail() {
  return getItem(LS_EMAIL_KEY) || '';
}

export function resetPersistedEmail() {
  setItem(LS_EMAIL_KEY, '');
}
