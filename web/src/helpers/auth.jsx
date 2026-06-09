/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useEffect } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { UserContext } from '../context/User';
import Loading from '../components/common/ui/Loading';
import { redirectToIAMLogin } from './utils';

export function authHeader() {
  // return authorization header with jwt token
  let user = JSON.parse(localStorage.getItem('user'));

  if (user && user.token) {
    return { Authorization: 'Bearer ' + user.token };
  } else {
    return {};
  }
}

export const AuthRedirect = ({ children }) => {
  const [userState] = React.useContext(UserContext);

  if (!userState?.authChecked) {
    return <Loading />;
  }

  if (userState?.user) {
    return <Navigate to='/console' replace />;
  }

  return children;
};

const IAMLoginRedirect = () => {
  const location = useLocation();

  useEffect(() => {
    const next = `${location.pathname}${location.search}${location.hash}`;
    redirectToIAMLogin(next, true);
  }, [location]);

  return <Loading />;
};

function PrivateRoute({ children }) {
  const [userState] = React.useContext(UserContext);

  if (!userState?.authChecked) {
    return <Loading />;
  }

  if (!userState?.user) {
    return <IAMLoginRedirect />;
  }
  return children;
}

export function AdminRoute({ children }) {
  const [userState] = React.useContext(UserContext);

  if (!userState?.authChecked) {
    return <Loading />;
  }

  if (!userState?.user) {
    return <IAMLoginRedirect />;
  }

  if (
    userState.user &&
    typeof userState.user.role === 'number' &&
    userState.user.role >= 10
  ) {
    return children;
  }

  return <Navigate to='/forbidden' replace />;
}

export { PrivateRoute };
