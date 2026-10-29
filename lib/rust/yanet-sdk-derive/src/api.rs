//! C export generation for authored module APIs.
//!
//! Public inherent methods receive native argument conversion, panic
//! containment and a trailing native error output. Mixed-site expansion spans
//! keep generated unsafe operations separate from the authored crate,
//! preserving its unsafe-code prohibition.

use proc_macro2::{Span, TokenStream};
use quote::{format_ident, quote_spanned};
use syn::{
    FnArg, Ident, ImplItem, ImplItemFn, ItemImpl, Lifetime, Path, ReturnType, Type, TypeReference, Visibility,
    parse::{Parse, ParseStream},
    spanned::Spanned,
};

/// The attribute: `module = <path>`.
pub struct Args {
    module: Path,
}

impl Parse for Args {
    fn parse(input: ParseStream) -> syn::Result<Self> {
        let key: Ident = input.parse()?;
        if key != "module" {
            return Err(syn::Error::new(key.span(), "expected `module = <Module type>`"));
        }
        input.parse::<syn::Token![=]>()?;
        let module = input.parse()?;
        if !input.is_empty() {
            return Err(input.error("unexpected tokens after the module"));
        }
        Ok(Self { module })
    }
}

const INTS: [&str; 9] = ["u8", "u16", "u32", "u64", "i8", "i16", "i32", "i64", "usize"];

enum Lowered<'a> {
    Agent,
    Str,
    Record(&'a Type),
    Slice(&'a Type),
    Int(&'a Type),
}

fn simple_ident(ty: &Type) -> Option<&Ident> {
    match ty {
        Type::Path(path) if path.qself.is_none() => path.path.segments.last().map(|segment| &segment.ident),
        _ => None,
    }
}

fn lower(ty: &Type) -> syn::Result<Lowered<'_>> {
    match ty {
        Type::Reference(TypeReference { mutability: Some(_), .. }) => Err(syn::Error::new(
            ty.span(),
            "a mutable reference does not cross the C boundary: take `&T`",
        )),
        Type::Reference(TypeReference {
            lifetime: Some(Lifetime { ident, .. }),
            ..
        }) if ident != "_" => Err(syn::Error::new(
            ty.span(),
            "a named lifetime lets the method keep what C lent for the call: elide it",
        )),
        Type::Reference(reference) => Ok(match &*reference.elem {
            elem if simple_ident(elem).is_some_and(|ident| ident == "Agent") => Lowered::Agent,
            elem if simple_ident(elem).is_some_and(|ident| ident == "str") => Lowered::Str,
            Type::Slice(slice) => Lowered::Slice(&slice.elem),
            elem => Lowered::Record(elem),
        }),
        Type::Path(_) if simple_ident(ty).is_some_and(|ident| INTS.iter().any(|int| ident == int)) => {
            Ok(Lowered::Int(ty))
        }
        _ => Err(syn::Error::new(
            ty.span(),
            "unsupported parameter type: use `&Agent`, `&str`, `&Record`, `&[Record]` or u8..u64, i8..i64, usize",
        )),
    }
}

/// Snake case of a CamelCase identifier: each upper case letter after the
/// first starts a word.
fn snake(name: &str) -> String {
    let mut out = String::new();
    for (index, ch) in name.chars().enumerate() {
        if ch.is_ascii_uppercase() {
            if index != 0 {
                out.push('_');
            }
            out.push(ch.to_ascii_lowercase());
        } else {
            out.push(ch);
        }
    }
    out
}

fn export(
    stem: &str,
    module: &Path,
    self_ty: &Ident,
    method: &ImplItemFn,
    p: &TokenStream,
    core: &Ident,
) -> syn::Result<TokenStream> {
    let ms = Span::mixed_site();
    let sig = &method.sig;
    if sig.receiver().is_some() {
        return Err(syn::Error::new(
            sig.span(),
            "an api method is an associated function: it has no `self`",
        ));
    }
    if sig.constness.is_some() || sig.asyncness.is_some() || sig.unsafety.is_some() || sig.abi.is_some() {
        return Err(syn::Error::new(sig.span(), "an api method is a plain `fn`"));
    }
    if !sig.generics.params.is_empty() || sig.generics.where_clause.is_some() || sig.variadic.is_some() {
        return Err(syn::Error::new(sig.generics.span(), "an api method is not generic"));
    }
    let name = &sig.ident;
    if name == "free" {
        return Err(syn::Error::new(
            name.span(),
            "`free` is the export the macro adds to the impl",
        ));
    }
    let symbol = Ident::new(&format!("{stem}_{name}"), ms);

    let mut agent_params = Vec::new();
    let mut c_params = Vec::new();
    let mut convert = Vec::new();
    let mut args = Vec::new();
    for (index, arg) in sig.inputs.iter().enumerate() {
        let FnArg::Typed(arg) = arg else {
            unreachable!("receivers are refused above")
        };
        let ty = &*arg.ty;
        let c = Ident::new(&format!("__a{index}"), ms);
        let v = Ident::new(&format!("__v{index}"), ms);
        let label = match &*arg.pat {
            syn::Pat::Ident(pat) => pat.ident.to_string(),
            _ => format!("#{index}"),
        };
        match lower(ty)? {
            Lowered::Agent => {
                agent_params.push(c.clone());
                c_params.push(quote_spanned!(ms=> #c: *mut #p::agent));
                args.push(quote_spanned!(ms=> __agent));
                continue;
            }
            Lowered::Str => {
                c_params.push(quote_spanned!(ms=> #c: *const #core::ffi::c_char));
                convert.push(quote_spanned!(ms=> let #v = #p::c_str(#c, #label)?;));
            }
            Lowered::Record(record) => {
                c_params.push(quote_spanned!(ms=> #c: *const #record));
                convert.push(quote_spanned!(record.span()=> let #v = #p::record::<#record>(#c, #label)?;));
            }
            Lowered::Slice(record) => {
                let len = Ident::new(&format!("__a{index}_len"), ms);
                c_params.push(quote_spanned!(ms=> #c: *const #record, #len: usize));
                convert.push(quote_spanned!(record.span()=> let #v = #p::slice_of::<#record>(#c, #len, #label)?;));
            }
            Lowered::Int(int) => {
                let int = simple_ident(int).expect("integer path checked above");
                c_params.push(quote_spanned!(ms=> #c: #core::primitive::#int));
                args.push(quote_spanned!(ms=> #c));
                continue;
            }
        }
        args.push(quote_spanned!(ms=> #v));
    }
    let [agent_param] = agent_params.as_slice() else {
        return Err(syn::Error::new(sig.span(), "an api method takes exactly one `&Agent`"));
    };

    // The annotation makes another module's configuration, or any other result
    // type, a type mismatch reported at the return type.
    let ret_span = match &sig.output {
        ReturnType::Type(_, ty) => ty.span(),
        ReturnType::Default => name.span(),
    };
    let ret = quote_spanned! {ret_span=>
        #core::result::Result<#p::Built<#module, #p::Agent>, #p::ApiError>
    };
    let doc = format!(" C export of `{self_ty}::{name}`.");
    Ok(quote_spanned! {ms=>
        #[doc = #doc]
        #[unsafe(no_mangle)]
        pub unsafe extern "C" fn #symbol(
            #(#c_params,)*
            __err: *mut *mut #p::yanet_error,
        ) -> *mut #p::cp_module {
            unsafe {
                #p::build::<#module>(#agent_param, __err, |__agent| {
                    #(#convert)*
                    let __built: #ret = #self_ty::#name(#(#args),*);
                    __built
                })
            }
        }
    })
}

pub fn expand(args: Args, item: ItemImpl) -> syn::Result<TokenStream> {
    let ms = Span::mixed_site();
    if item.trait_.is_some() || !item.generics.params.is_empty() || item.generics.where_clause.is_some() {
        return Err(syn::Error::new(
            item.impl_token.span(),
            "#[api] goes on an inherent, non-generic impl of a local type",
        ));
    }
    let self_ty = match &*item.self_ty {
        Type::Path(path) if path.qself.is_none() && path.path.get_ident().is_some() => path.path.get_ident(),
        _ => None,
    };
    let Some(self_ty) = self_ty else {
        return Err(syn::Error::new(
            item.self_ty.span(),
            "#[api] needs a local type named by its identifier",
        ));
    };
    let crate_name = std::env::var("CARGO_CRATE_NAME").map_err(|_| {
        syn::Error::new(
            Span::call_site(),
            "#[api] needs CARGO_CRATE_NAME: build the api crate with cargo",
        )
    })?;
    if crate_name.contains("__") || crate_name.ends_with('_') {
        return Err(syn::Error::new(
            Span::call_site(),
            format!(
                "the api crate name `{crate_name}` contains `__` or ends in `_`: its export prefix would not be unique"
            ),
        ));
    }
    let stem = format!("yanet_cp_{crate_name}__{}", snake(&self_ty.to_string()));
    let module = &args.module;
    let sdk = format_ident!("__yanet_api_for_{}", self_ty);
    let core = format_ident!("__yanet_api_core_for_{}", self_ty);
    let p = quote_spanned!(ms=> #sdk::__private);
    let exports = item
        .items
        .iter()
        .filter_map(|member| match member {
            ImplItem::Fn(method) if matches!(method.vis, Visibility::Public(_)) => Some(method),
            _ => None,
        })
        .map(|method| export(&stem, module, self_ty, method, &p, &core))
        .collect::<syn::Result<Vec<_>>>()?;
    let free = Ident::new(&format!("{stem}_free"), ms);
    let doc = format!(" Frees a configuration `{self_ty}` built, as a C module's free does.");
    Ok(quote_spanned! {ms=>
        #[allow(non_snake_case)]
        extern crate yanet_sdk_api as #sdk;
        #[allow(non_snake_case)]
        extern crate core as #core;
        #item

        #(#exports)*

        #[doc = #doc]
        #[unsafe(no_mangle)]
        pub unsafe extern "C" fn #free(
            __module: *mut #p::cp_module,
            __err: *mut *mut #p::yanet_error,
        ) -> #core::ffi::c_int {
            unsafe { #p::free::<#module>(__module, __err) }
        }
    })
}
