package controller

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

const (
	iamSessionAccountNoKey     = "iam_account_no"
	iamSessionProfileIDKey     = "iam_profile_id"
	iamSessionUserNameKey      = "iam_user_name"
	iamSessionEmailKey         = "iam_email"
	iamSessionMobileKey        = "iam_mobile"
	defaultIAMStateTTL         = 5 * time.Minute
	defaultSessionMaxAgeSecond = 2592000
	defaultLogoutRedirectParam = "logout_redirect"
)

type iamConfig struct {
	ClientID            string
	ClientSecret        string
	RedirectURI         string
	AuthorizeURL        string
	TokenURL            string
	ProfileURL          string
	ValidateURL         string
	LogoutURL           string
	Scope               string
	LogoutRedirectParam string
}

type iamStateClaims struct {
	Next string `json:"next"`
	jwt.RegisteredClaims
}

type iamTokenResult struct {
	AccessToken string
	ExpiresIn   int64
}

type iamProfile struct {
	ID        string
	AccountNo string
	UserName  string
	Email     string
	Mobile    string
}

func IAMLogin(c *gin.Context) {
	cfg, err := getIAMConfig()
	if err != nil {
		common.SysError("iam login requested but configuration is incomplete: " + err.Error())
		c.String(http.StatusServiceUnavailable, err.Error())
		return
	}
	next := sanitizeReturnTarget(c, c.Query("next"), "/")
	state, err := signIAMState(next)
	if err != nil {
		common.SysError("failed to sign iam state: " + err.Error())
		c.String(http.StatusInternalServerError, "failed to initialize iam login")
		return
	}
	authorizeURL, err := buildIAMAuthorizeURL(cfg, state)
	if err != nil {
		common.SysError("failed to build iam authorize url: " + err.Error())
		c.String(http.StatusInternalServerError, "failed to initialize iam login")
		return
	}
	c.Redirect(http.StatusFound, authorizeURL)
}

func IAMCallback(c *gin.Context) {
	providerErr := strings.TrimSpace(c.Query("error"))
	if providerErr != "" {
		detail := strings.TrimSpace(c.Query("error_description"))
		if detail != "" {
			providerErr += ": " + detail
		}
		c.String(http.StatusBadRequest, providerErr)
		return
	}

	code := strings.TrimSpace(c.Query("code"))
	stateValue := strings.TrimSpace(c.Query("state"))
	if code == "" || stateValue == "" {
		c.String(http.StatusBadRequest, "missing code or state")
		return
	}

	cfg, err := getIAMConfig()
	if err != nil {
		common.SysError("iam callback requested but configuration is incomplete: " + err.Error())
		c.String(http.StatusServiceUnavailable, err.Error())
		return
	}

	stateClaims, err := parseIAMState(stateValue)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid state")
		return
	}
	redirectTarget := stateClaims.Next
	if redirectTarget == "" {
		redirectTarget = "/"
	}

	tokenResult, err := exchangeIAMToken(c.Request.Context(), cfg, code)
	if err != nil {
		common.SysError("failed to exchange iam token: " + err.Error())
		c.String(http.StatusBadGateway, "failed to exchange iam token")
		return
	}

	if err := validateIAMToken(c.Request.Context(), cfg, tokenResult.AccessToken); err != nil {
		common.SysError("iam token validation reported invalid token: " + err.Error())
		c.String(http.StatusUnauthorized, "invalid iam token")
		return
	}

	profile, err := fetchIAMProfile(c.Request.Context(), cfg, tokenResult.AccessToken)
	if err != nil {
		common.SysError("failed to fetch iam profile: " + err.Error())
		c.String(http.StatusBadGateway, "failed to fetch iam profile")
		return
	}

	user, err := resolveIAMUser(profile)
	if err != nil {
		common.SysError("failed to resolve local user for iam profile: " + err.Error())
		c.String(http.StatusInternalServerError, "failed to resolve local user")
		return
	}
	if user.Status != common.UserStatusEnabled {
		c.String(http.StatusForbidden, "user is disabled")
		return
	}

	session := sessions.Default(c)
	populateLoginSession(session, user)
	session.Set(iamSessionProfileIDKey, firstNonEmpty(profile.ID, strconv.Itoa(user.Id)))
	session.Set(iamSessionAccountNoKey, firstNonEmpty(profile.AccountNo, user.Username))
	session.Set(iamSessionUserNameKey, firstNonEmpty(profile.UserName, user.DisplayName, user.Username))
	session.Set(iamSessionEmailKey, firstNonEmpty(profile.Email, user.Email))
	session.Set(iamSessionMobileKey, profile.Mobile)
	if err := session.Save(); err != nil {
		common.SysError("failed to save iam session: " + err.Error())
		c.String(http.StatusInternalServerError, "failed to save session")
		return
	}

	c.Redirect(http.StatusFound, redirectTarget)
}

func IAMLogout(c *gin.Context) {
	returnTo := sanitizeReturnTarget(c, c.Query("returnTo"), "/")
	if err := clearLoginSession(c); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	cfg, err := getIAMConfig()
	if err == nil && cfg.LogoutURL != "" {
		logoutURL, buildErr := buildIAMLogoutURL(c, cfg, returnTo)
		if buildErr == nil {
			c.Redirect(http.StatusFound, logoutURL)
			return
		}
		common.SysError("failed to build iam logout url: " + buildErr.Error())
	}

	c.Redirect(http.StatusFound, returnTo)
}

func GetIAMMe(c *gin.Context) {
	session := sessions.Default(c)
	userID, ok := sessionInt(session.Get("id"))
	if !ok || userID == 0 {
		c.JSON(http.StatusOK, gin.H{"loggedIn": false})
		return
	}

	user, err := model.GetUserById(userID, false)
	if err != nil || user.Status != common.UserStatusEnabled {
		_ = clearLoginSession(c)
		c.JSON(http.StatusOK, gin.H{"loggedIn": false})
		return
	}

	loginAt, _ := sessionInt64(session.Get("login_at"))
	expiresIn := int64(defaultSessionMaxAgeSecond)
	if loginAt > 0 {
		remaining := int64(defaultSessionMaxAgeSecond) - (time.Now().Unix() - loginAt)
		if remaining < 0 {
			remaining = 0
		}
		expiresIn = remaining
	}

	accountNo := firstNonEmpty(sessionString(session.Get(iamSessionAccountNoKey)), user.Username, strconv.Itoa(user.Id))
	displayName := firstNonEmpty(sessionString(session.Get(iamSessionUserNameKey)), user.DisplayName, user.Username)
	email := firstNonEmpty(sessionString(session.Get(iamSessionEmailKey)), user.Email)
	mobile := sessionString(session.Get(iamSessionMobileKey))
	profileID := firstNonEmpty(sessionString(session.Get(iamSessionProfileIDKey)), strconv.Itoa(user.Id))
	localUser := buildUserSelfResponseData(user, user.Role)

	c.JSON(http.StatusOK, gin.H{
		"loggedIn":  true,
		"expiresIn": expiresIn,
		"localUser": localUser,
		"user": gin.H{
			"id":         profileID,
			"account_no": accountNo,
			"attributes": gin.H{
				"account_no": accountNo,
				"user_name":  displayName,
				"email":      email,
				"mobile":     mobile,
			},
		},
	})
}

func getIAMConfig() (*iamConfig, error) {
	cfg := &iamConfig{
		ClientID:            strings.TrimSpace(os.Getenv("SSO_CLIENT_ID")),
		ClientSecret:        strings.TrimSpace(os.Getenv("SSO_CLIENT_SECRET")),
		RedirectURI:         strings.TrimSpace(os.Getenv("SSO_REDIRECT_URI")),
		AuthorizeURL:        strings.TrimSpace(os.Getenv("SSO_AUTHORIZE_URL")),
		TokenURL:            strings.TrimSpace(os.Getenv("SSO_TOKEN_URL")),
		ProfileURL:          strings.TrimSpace(os.Getenv("SSO_PROFILE_URL")),
		ValidateURL:         strings.TrimSpace(os.Getenv("SSO_VALIDATE_URL")),
		LogoutURL:           strings.TrimSpace(os.Getenv("SSO_LOGOUT_URL")),
		Scope:               strings.TrimSpace(os.Getenv("SSO_SCOPE")),
		LogoutRedirectParam: strings.TrimSpace(os.Getenv("SSO_LOGOUT_REDIRECT_PARAM")),
	}
	if cfg.LogoutRedirectParam == "" {
		cfg.LogoutRedirectParam = defaultLogoutRedirectParam
	}
	missing := make([]string, 0)
	if cfg.ClientID == "" {
		missing = append(missing, "SSO_CLIENT_ID")
	}
	if cfg.ClientSecret == "" {
		missing = append(missing, "SSO_CLIENT_SECRET")
	}
	if cfg.RedirectURI == "" {
		missing = append(missing, "SSO_REDIRECT_URI")
	}
	if cfg.AuthorizeURL == "" {
		missing = append(missing, "SSO_AUTHORIZE_URL")
	}
	if cfg.TokenURL == "" {
		missing = append(missing, "SSO_TOKEN_URL")
	}
	if cfg.ProfileURL == "" {
		missing = append(missing, "SSO_PROFILE_URL")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing IAM environment variables: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

func signIAMState(next string) (string, error) {
	claims := iamStateClaims{
		Next: next,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        common.GetUUID(),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(defaultIAMStateTTL)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(common.SessionSecret))
}

func parseIAMState(raw string) (*iamStateClaims, error) {
	claims := &iamStateClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
		}
		return []byte(common.SessionSecret), nil
	})
	if err != nil {
		return nil, err
	}
	if !parsed.Valid {
		return nil, fmt.Errorf("invalid state token")
	}
	return claims, nil
}

func buildIAMAuthorizeURL(cfg *iamConfig, state string) (string, error) {
	parsed, err := url.Parse(cfg.AuthorizeURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", cfg.ClientID)
	query.Set("redirect_uri", cfg.RedirectURI)
	query.Set("state", state)
	if cfg.Scope != "" {
		query.Set("scope", cfg.Scope)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func buildIAMLogoutURL(c *gin.Context, cfg *iamConfig, returnTo string) (string, error) {
	parsed, err := url.Parse(cfg.LogoutURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set(cfg.LogoutRedirectParam, absoluteReturnTarget(c, returnTo))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func exchangeIAMToken(ctx context.Context, cfg *iamConfig, code string) (*iamTokenResult, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("redirect_uri", cfg.RedirectURI)
	form.Set("code", code)

	responseBody, err := doIAMFormRequest(ctx, http.MethodPost, cfg.TokenURL, form, nil)
	if err != nil {
		responseBody, err = doIAMFormRequest(ctx, http.MethodGet, cfg.TokenURL, form, nil)
		if err != nil {
			return nil, err
		}
	}
	return parseIAMTokenResponse(responseBody)
}

func validateIAMToken(ctx context.Context, cfg *iamConfig, accessToken string) error {
	if cfg.ValidateURL == "" || accessToken == "" {
		return nil
	}
	body, err := doIAMJSONRequest(ctx, http.MethodGet, cfg.ValidateURL, url.Values{
		"access_token": []string{accessToken},
		"accessToken":  []string{accessToken},
	}, map[string]string{
		"Authorization": "Bearer " + accessToken,
	})
	if err != nil {
		common.SysError("iam validate request failed, continuing without hard failure: " + err.Error())
		return nil
	}
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		common.SysError("iam validate response is not json, continuing without hard failure: " + err.Error())
		return nil
	}
	if isIAMTokenExplicitlyInvalid(payload) {
		return fmt.Errorf("iam validate endpoint reported invalid token")
	}
	return nil
}

func fetchIAMProfile(ctx context.Context, cfg *iamConfig, accessToken string) (*iamProfile, error) {
	attempts := []func() ([]byte, error){
		func() ([]byte, error) {
			return doIAMJSONRequest(ctx, http.MethodGet, cfg.ProfileURL, nil, map[string]string{
				"Authorization": "Bearer " + accessToken,
			})
		},
		func() ([]byte, error) {
			return doIAMJSONRequest(ctx, http.MethodGet, cfg.ProfileURL, url.Values{
				"access_token": []string{accessToken},
				"accessToken":  []string{accessToken},
			}, nil)
		},
		func() ([]byte, error) {
			return doIAMFormRequest(ctx, http.MethodPost, cfg.ProfileURL, url.Values{
				"access_token": []string{accessToken},
				"accessToken":  []string{accessToken},
			}, nil)
		},
	}

	var firstErr error
	for _, attempt := range attempts {
		body, err := attempt()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		profile, parseErr := parseIAMProfile(body)
		if parseErr == nil {
			return profile, nil
		}
		if firstErr == nil {
			firstErr = parseErr
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("iam profile request failed")
	}
	return nil, firstErr
}

func doIAMJSONRequest(ctx context.Context, method string, rawURL string, params url.Values, headers map[string]string) ([]byte, error) {
	return doIAMRequest(ctx, method, rawURL, params, nil, headers)
}

func doIAMFormRequest(ctx context.Context, method string, rawURL string, form url.Values, headers map[string]string) ([]byte, error) {
	localHeaders := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	}
	for key, value := range headers {
		localHeaders[key] = value
	}
	if method == http.MethodGet || method == http.MethodHead {
		return doIAMRequest(ctx, method, rawURL, form, nil, localHeaders)
	}
	return doIAMRequest(ctx, method, rawURL, nil, strings.NewReader(form.Encode()), localHeaders)
}

func doIAMRequest(ctx context.Context, method string, rawURL string, params url.Values, body io.Reader, headers map[string]string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if params != nil && (method == http.MethodGet || method == http.MethodHead) {
		query := parsed.Query()
		for key, values := range params {
			for _, value := range values {
				query.Set(key, value)
			}
		}
		parsed.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	client := service.GetHttpClient()
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("iam endpoint %s returned %d: %s", parsed.String(), response.StatusCode, safeResponsePreview(responseBody))
	}
	return responseBody, nil
}

func parseIAMTokenResponse(body []byte) (*iamTokenResult, error) {
	if values, err := url.ParseQuery(string(body)); err == nil {
		accessToken := strings.TrimSpace(values.Get("access_token"))
		if accessToken == "" {
			accessToken = strings.TrimSpace(values.Get("accessToken"))
		}
		if accessToken != "" {
			expiresIn, _ := strconv.ParseInt(firstNonEmpty(values.Get("expires_in"), values.Get("expiresIn")), 10, 64)
			return &iamTokenResult{AccessToken: accessToken, ExpiresIn: expiresIn}, nil
		}
	}

	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	accessToken := firstNonEmpty(
		lookupString(payload, "access_token"),
		lookupString(payload, "accessToken"),
		lookupString(payload, "token"),
		lookupString(payload, "data.access_token"),
		lookupString(payload, "data.accessToken"),
		lookupString(payload, "result.access_token"),
		lookupString(payload, "result.accessToken"),
	)
	if accessToken == "" {
		return nil, fmt.Errorf("access token missing in iam response")
	}
	expiresIn := firstNonZeroInt64(
		lookupInt64(payload, "expires_in"),
		lookupInt64(payload, "expiresIn"),
		lookupInt64(payload, "data.expires_in"),
		lookupInt64(payload, "data.expiresIn"),
		lookupInt64(payload, "result.expires_in"),
		lookupInt64(payload, "result.expiresIn"),
	)
	return &iamTokenResult{AccessToken: accessToken, ExpiresIn: expiresIn}, nil
}

func parseIAMProfile(body []byte) (*iamProfile, error) {
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	profile := &iamProfile{
		ID: firstNonEmpty(
			lookupString(payload, "id"),
			lookupString(payload, "user_id"),
			lookupString(payload, "userId"),
			lookupString(payload, "sub"),
			lookupString(payload, "subject"),
			lookupString(payload, "data.id"),
			lookupString(payload, "data.user_id"),
			lookupString(payload, "data.userId"),
			lookupString(payload, "result.id"),
		),
		AccountNo: firstNonEmpty(
			lookupString(payload, "account_no"),
			lookupString(payload, "accountNo"),
			lookupString(payload, "attributes.account_no"),
			lookupString(payload, "data.account_no"),
			lookupString(payload, "data.accountNo"),
			lookupString(payload, "data.attributes.account_no"),
			lookupString(payload, "result.account_no"),
			lookupString(payload, "result.accountNo"),
			lookupString(payload, "result.attributes.account_no"),
		),
		UserName: firstNonEmpty(
			lookupString(payload, "user_name"),
			lookupString(payload, "userName"),
			lookupString(payload, "name"),
			lookupString(payload, "display_name"),
			lookupString(payload, "displayName"),
			lookupString(payload, "attributes.user_name"),
			lookupString(payload, "data.user_name"),
			lookupString(payload, "data.userName"),
			lookupString(payload, "data.name"),
			lookupString(payload, "data.attributes.user_name"),
			lookupString(payload, "result.user_name"),
			lookupString(payload, "result.userName"),
			lookupString(payload, "result.name"),
			lookupString(payload, "result.attributes.user_name"),
		),
		Email: firstNonEmpty(
			lookupString(payload, "email"),
			lookupString(payload, "attributes.email"),
			lookupString(payload, "data.email"),
			lookupString(payload, "data.attributes.email"),
			lookupString(payload, "result.email"),
			lookupString(payload, "result.attributes.email"),
		),
		Mobile: firstNonEmpty(
			lookupString(payload, "mobile"),
			lookupString(payload, "phone"),
			lookupString(payload, "attributes.mobile"),
			lookupString(payload, "data.mobile"),
			lookupString(payload, "data.phone"),
			lookupString(payload, "data.attributes.mobile"),
			lookupString(payload, "result.mobile"),
			lookupString(payload, "result.phone"),
			lookupString(payload, "result.attributes.mobile"),
		),
	}
	if profile.AccountNo == "" {
		profile.AccountNo = firstNonEmpty(profile.ID, profile.Email)
	}
	if profile.ID == "" {
		profile.ID = firstNonEmpty(profile.AccountNo, profile.Email)
	}
	if profile.UserName == "" {
		profile.UserName = firstNonEmpty(profile.AccountNo, profile.Email)
	}
	profile.AccountNo = strings.TrimSpace(profile.AccountNo)
	profile.ID = strings.TrimSpace(profile.ID)
	profile.UserName = strings.TrimSpace(profile.UserName)
	profile.Email = strings.TrimSpace(profile.Email)
	profile.Mobile = strings.TrimSpace(profile.Mobile)
	if profile.ID == "" && profile.AccountNo == "" && profile.Email == "" && profile.UserName == "" {
		return nil, fmt.Errorf("iam profile payload does not contain usable user identifiers")
	}
	return profile, nil
}

func resolveIAMUser(profile *iamProfile) (*model.User, error) {
	user, err := findIAMUser(profile)
	if err != nil {
		return nil, err
	}
	if user != nil {
		if err := syncIAMUserFields(user, profile); err != nil {
			return nil, err
		}
		return user, nil
	}

	username := generateUniqueIAMUsername(profile)
	newUser := &model.User{
		Username:    username,
		Password:    common.GetUUID(),
		DisplayName: trimRunes(firstNonEmpty(profile.UserName, profile.AccountNo, username), 20),
		Email:       trimRunes(profile.Email, 50),
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if newUser.DisplayName == "" {
		newUser.DisplayName = newUser.Username
	}
	if err := newUser.Insert(0); err != nil {
		return nil, err
	}
	return newUser, nil
}

func findIAMUser(profile *iamProfile) (*model.User, error) {
	if profile.Email != "" {
		var user model.User
		err := model.DB.Where("email = ?", profile.Email).First(&user).Error
		if err == nil {
			return &user, nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	stableUsername := buildStableIAMUsername(profile)
	if stableUsername == "" {
		return nil, nil
	}
	var user model.User
	err := model.DB.Where("username = ?", stableUsername).First(&user).Error
	if err == nil {
		return &user, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return nil, nil
}

func syncIAMUserFields(user *model.User, profile *iamProfile) error {
	changed := false
	desiredDisplayName := trimRunes(firstNonEmpty(profile.UserName, profile.AccountNo, user.DisplayName, user.Username), 20)
	if desiredDisplayName != "" && user.DisplayName != desiredDisplayName {
		user.DisplayName = desiredDisplayName
		changed = true
	}
	desiredEmail := trimRunes(profile.Email, 50)
	if desiredEmail != "" && user.Email != desiredEmail {
		user.Email = desiredEmail
		changed = true
	}
	if !changed {
		return nil
	}
	return user.Update(false)
}

func generateUniqueIAMUsername(profile *iamProfile) string {
	base := buildStableIAMUsername(profile)
	if base == "" {
		base = trimRunes("iam_"+shortHash(common.GetUUID()), model.UserNameMaxLength)
	}
	if !iamUsernameExists(base) {
		return base
	}
	for i := 1; i < 100; i++ {
		candidate := buildStableIAMUsernameWithSalt(profile, i)
		if candidate != "" && !iamUsernameExists(candidate) {
			return candidate
		}
	}
	return trimRunes("iam_"+shortHash(common.GetUUID()), model.UserNameMaxLength)
}

func buildStableIAMUsername(profile *iamProfile) string {
	key := firstNonEmpty(profile.AccountNo, profile.ID, emailLocalPart(profile.Email), profile.UserName)
	return buildIAMUsernameFromKey(key, "")
}

func buildStableIAMUsernameWithSalt(profile *iamProfile, salt int) string {
	key := firstNonEmpty(profile.AccountNo, profile.ID, emailLocalPart(profile.Email), profile.UserName)
	return buildIAMUsernameFromKey(key, strconv.Itoa(salt))
}

func buildIAMUsernameFromKey(key string, salt string) string {
	key = strings.TrimSpace(key)
	candidate := normalizeIAMUsernameCandidate(key)
	if salt == "" && candidate != "" && runeLength(candidate) <= model.UserNameMaxLength {
		return candidate
	}
	if key == "" {
		key = common.GetUUID()
	}
	if salt != "" {
		key += ":" + salt
	}
	return trimRunes("iam_"+shortHash(key), model.UserNameMaxLength)
}

func normalizeIAMUsernameCandidate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range raw {
		switch r {
		case ' ', '\t', '\n', '\r', '/', '\\', '?', '#', '%', '&', '=':
			builder.WriteByte('_')
		default:
			builder.WriteRune(r)
		}
	}
	candidate := strings.Trim(builder.String(), "._-")
	if candidate == "" {
		return ""
	}
	if runeLength(candidate) > model.UserNameMaxLength {
		return ""
	}
	return candidate
}

func iamUsernameExists(username string) bool {
	var count int64
	model.DB.Unscoped().Model(&model.User{}).Where("username = ?", username).Count(&count)
	return count > 0
}

func sanitizeReturnTarget(c *gin.Context, raw string, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fallback
	}
	if !parsed.IsAbs() {
		return fallback
	}
	if strings.EqualFold(parsed.Host, requestHost(c)) && strings.EqualFold(parsed.Scheme, requestScheme(c)) {
		return raw
	}
	return fallback
}

func absoluteReturnTarget(c *gin.Context, target string) string {
	target = sanitizeReturnTarget(c, target, "/")
	if strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//") {
		return fmt.Sprintf("%s://%s%s", requestScheme(c), requestHost(c), target)
	}
	return target
}

func requestScheme(c *gin.Context) string {
	if forwarded := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}
	if c.Request.TLS != nil {
		return "https"
	}
	return "http"
}

func requestHost(c *gin.Context) string {
	if forwarded := strings.TrimSpace(c.GetHeader("X-Forwarded-Host")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}
	return c.Request.Host
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	hash := fmt.Sprintf("%x", sum)
	if len(hash) > 16 {
		return hash[:16]
	}
	return hash
}

func trimRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max])
}

func runeLength(value string) int {
	return len([]rune(value))
}

func emailLocalPart(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return ""
	}
	parts := strings.SplitN(email, "@", 2)
	return parts[0]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func sessionInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		i, err := strconv.Atoi(v)
		return i, err == nil
	default:
		return 0, false
	}
}

func sessionInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case string:
		i, err := strconv.ParseInt(v, 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

func sessionString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func safeResponsePreview(body []byte) string {
	preview := strings.TrimSpace(string(body))
	if runeLength(preview) > 200 {
		preview = trimRunes(preview, 200)
	}
	return preview
}

func lookupValue(payload map[string]any, path string) (any, bool) {
	current := any(payload)
	for _, segment := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func lookupString(payload map[string]any, path string) string {
	value, ok := lookupValue(payload, path)
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strings.TrimSpace(fmt.Sprint(v))
	default:
		return ""
	}
}

func lookupInt64(payload map[string]any, path string) int64 {
	value, ok := lookupValue(payload, path)
	if !ok || value == nil {
		return 0
	}
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err == nil {
			return i
		}
	}
	return 0
}

func lookupBool(payload map[string]any, path string) (bool, bool) {
	value, ok := lookupValue(payload, path)
	if !ok || value == nil {
		return false, false
	}
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		return b, err == nil
	default:
		return false, false
	}
}

func isIAMTokenExplicitlyInvalid(payload map[string]any) bool {
	for _, path := range []string{"valid", "data.valid", "result.valid"} {
		if valid, ok := lookupBool(payload, path); ok && !valid {
			return true
		}
	}
	for _, path := range []string{"success", "data.success", "result.success"} {
		if success, ok := lookupBool(payload, path); ok && !success {
			return true
		}
	}
	return false
}
