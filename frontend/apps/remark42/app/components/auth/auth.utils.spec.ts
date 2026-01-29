import { StaticStore } from 'common/static-store';

import { getProviders } from './auth.utils';

describe('getProviders', () => {
  const defaultProviders = StaticStore.config.auth_providers;

  afterAll(() => {
    StaticStore.config.auth_providers = defaultProviders;
  });

  it('should separate oauth and form providers', () => {
    StaticStore.config.auth_providers = ['google', 'email'];
    const [oauth, form] = getProviders();
    expect(oauth).toEqual(['google']);
    expect(form).toEqual(['email']);
  });

  it('should exclude jwt from both oauth and form providers', () => {
    StaticStore.config.auth_providers = ['jwt'];
    const [oauth, form] = getProviders();
    expect(oauth).toEqual([]);
    expect(form).toEqual([]);
  });

  it('should exclude forward_auth from both oauth and form providers', () => {
    StaticStore.config.auth_providers = ['forward_auth'];
    const [oauth, form] = getProviders();
    expect(oauth).toEqual([]);
    expect(form).toEqual([]);
  });

  it('should exclude jwt and forward_auth while keeping other providers', () => {
    StaticStore.config.auth_providers = ['google', 'jwt', 'email', 'forward_auth', 'github'];
    const [oauth, form] = getProviders();
    expect(oauth).toEqual(['google', 'github']);
    expect(form).toEqual(['email']);
  });

  it('should return empty arrays when only transparent providers are configured', () => {
    StaticStore.config.auth_providers = ['jwt', 'forward_auth'];
    const [oauth, form] = getProviders();
    expect(oauth).toEqual([]);
    expect(form).toEqual([]);
  });
});
