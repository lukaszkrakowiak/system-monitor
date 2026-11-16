import {useState} from 'react';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {HStack} from '@astryxdesign/core/HStack';
import {TextInput} from '@astryxdesign/core/TextInput';
import {VStack} from '@astryxdesign/core/VStack';
import {ApiError, setToken} from '../lib/api';

/** Błąd API; przy 401 pozwala podać token Bearer (dla Basic przeglądarka pokaże własne okno logowania). */
export function ErrorView({error}: {error: Error}) {
  const [token, setLocalToken] = useState('');
  const unauthorized = error instanceof ApiError && error.status === 401;
  if (!unauthorized) {
    return <Banner status="error" title="Request failed" description={error.message} />;
  }
  return (
    <VStack gap={3}>
      <Banner status="warning" title="Authentication required" description="Enter the API token to continue." />
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setToken(token.trim());
          window.location.reload();
        }}
      >
        <HStack gap={2} align="end">
          <TextInput label="API token" type="password" value={token} onChange={(v) => setLocalToken(v)} />
          <Button type="submit" label="Sign in" variant="primary" isDisabled={token.trim() === ''} />
        </HStack>
      </form>
    </VStack>
  );
}
